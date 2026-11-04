package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// completeInterview runs the plan to INTERVIEW_COMPLETED over HTTP with candidate speech reported as
// provider transcription (synthetic speech is never evaluated).
func (e *env) completeInterview() (id, cookie string) {
	e.t.Helper()
	id, cookie = e.interviewToReady()
	agent, _ := e.startAndClaim(id, cookie)
	base := e.internal.URL + "/internal/v1/tier1/sessions/" + id
	seg := func(key, source, engine string, startMs, endMs int64, text string) response {
		return e.do(req{method: "POST", url: base + "/transcript-segments", headers: agent.headers(), body: map[string]any{
			"segment_key": key, "speaker": "candidate", "text": text, "start_ms": startMs, "end_ms": endMs, "source": source,
			"confidence": nil, "language": "en", "interrupted": false, "provider_item_id": nil, "asr_model": nil, "engine": engine,
			"signals": map[string]any{"overlapped_interviewer_audio": false, "low_energy": false}}})
	}
	time.Sleep(30 * time.Millisecond)
	if r := seg("calib-0000001", "synthetic_test", "scripted", 0, 10, "hello"); r.status != 201 {
		e.t.Fatalf("calibration segment: %d %s", r.status, r.raw)
	}
	ctx := e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
	if r := e.do(req{method: "POST", url: base + "/calibration-complete", headers: agent.headers(), body: map[string]any{"state_version": ctx.num("state_version")}}); r.status != 200 {
		e.t.Fatalf("calibration-complete: %d %s", r.status, r.raw)
	}
	for i := 0; ; i++ {
		ctx = e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
		if ctx.str("state") == "INTERVIEW_COMPLETED" {
			return id, cookie
		}
		if i > 5 {
			e.t.Fatal("interview did not complete")
		}
		act := ctx.str("active_prompt", "activation_id")
		epoch := ctx.num("timeline", "epoch_unix_ms")
		now := func() int64 { return time.Now().UnixMilli() - epoch }
		if r := e.do(req{method: "POST", url: base + "/prompt-started", headers: agent.headers(), body: map[string]any{"activation_id": act, "at_ms": now()}}); r.status != 200 {
			e.t.Fatalf("prompt-started: %d %s", r.status, r.raw)
		}
		time.Sleep(20 * time.Millisecond)
		key := fmt.Sprintf("answer-%06d", i)
		if r := seg(key, "realtime_transcription", "openai_realtime", now()-5, now(), "Each update writes a new row version."); r.status != 201 {
			e.t.Fatalf("answer segment: %d %s", r.status, r.raw)
		}
		ctx = e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
		if r := e.do(req{method: "POST", url: base + "/answer-complete", headers: agent.headers(), body: map[string]any{
			"state_version": ctx.num("state_version"), "activation_id": act, "flushed_segment_keys": []string{key}, "cause": "agent_tool"}}); r.status != 200 {
			e.t.Fatalf("answer-complete: %d %s", r.status, r.raw)
		}
	}
}

func (e *env) staffToken(role string) string {
	e.t.Helper()
	tok := api.StaffTokenPrefix + ids.Token()
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,$4,'test',now() + interval '1 day')`,
		ids.New(ids.Staff), e.tenant, ids.Hash(tok), role); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

type storedAnswer struct {
	ID, Item, Segment, Text, Rubric string
	StartMs, EndMs                  int64
}

func (e *env) answers(id string) []storedAnswer {
	e.t.Helper()
	rows, err := e.db.Owner.Query(context.Background(), `SELECT a.id, a.item_id, s.id, s.text, s.start_ms, s.end_ms, q.rubric_id || '@' || q.rubric_version
		FROM ati.candidate_answers a JOIN ati.transcript_segments s ON s.id = a.segment_ids[1]
		JOIN ati.interview_questions q ON q.plan_id = 'plan_demo_backend_en' AND q.plan_version = 1 AND q.item_id = a.item_id
		WHERE a.session_id = $1 ORDER BY a.finalized_at`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []storedAnswer
	for rows.Next() {
		var a storedAnswer
		if err := rows.Scan(&a.ID, &a.Item, &a.Segment, &a.Text, &a.StartMs, &a.EndMs, &a.Rubric); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func evaluationOutput(id string, a storedAnswer) map[string]any {
	ev := func(dim string) map[string]any {
		return map[string]any{"dimension": dim, "transcript_start_ms": a.StartMs, "transcript_end_ms": a.EndMs, "transcript_segment_ids": []string{a.Segment},
			"quote": a.Text, "claim": "c", "assessment": "correct", "reference_ids": []string{}}
	}
	return map[string]any{
		"session_id": id, "question_id": a.Item, "evaluation_version": a.Rubric,
		"transcript_quality": map[string]any{"score": 0.9, "language": "en", "ambiguities": []string{}, "missing_segments": false},
		"scores":             map[string]int{"technical_accuracy": 50, "architecture_maturity": 50, "edge_case_awareness": 50, "claim_support": 50},
		"weighted_score":     50.0, "confidence": map[string]any{"score": 0.9, "calibrated": false, "reason": "r"},
		"evidence":         []any{ev("technical_accuracy"), ev("architecture_maturity"), ev("edge_case_awareness"), ev("claim_support")},
		"missing_concepts": []string{}, "contradictions": []string{}, "unsupported_claims": []string{},
		"hallucination_risk": map[string]any{"score": 0, "severity": "low", "evidence": []string{}}, "recommendation": "NEXT_QUESTION", "review_required": false,
	}
}

func TestTier2AndReviewEndpoints(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.completeInterview()
	base := e.internal.URL + "/internal/v1/tier2/sessions/" + id
	tier2 := map[string]string{"Authorization": "Bearer " + tier2Token}
	answers := e.answers(id)
	if len(answers) != 4 {
		t.Fatalf("answers: %d", len(answers))
	}
	abstain := func(a storedAnswer) map[string]any {
		return map[string]any{"answer_id": a.ID, "transcript_version": 1, "evaluator_version": "reference@1", "provider": "reference", "outcome": "abstained", "reason": "transcript_quality"}
	}
	// Only the Tier-2 credential reaches the Tier-2 endpoints, and it reaches nothing else.
	for _, h := range []map[string]string{nil, {"Authorization": "Bearer " + agentToken}, {"Authorization": "Bearer " + e.staff}} {
		if r := e.do(req{method: "POST", url: base + "/evaluations", headers: h, body: abstain(answers[0])}); r.status != 401 {
			t.Fatalf("Tier-2 endpoint without the Tier-2 credential: %d", r.status)
		}
	}
	if r := e.do(req{method: "GET", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/context", headers: tier2}); r.status != 401 {
		t.Fatalf("the Tier-2 credential is not an agent credential: %d", r.status)
	}
	// INV-6: nothing before the evaluation is enqueued (T14, applied by the outbox relay).
	if r := e.do(req{method: "POST", url: base + "/evaluations", headers: tier2, body: abstain(answers[0])}); r.status != 409 || r.str("code") != "evaluation_not_pending" {
		t.Fatalf("before T14: %d %s", r.status, r.raw)
	}
	if err := e.svc.EnqueueEvaluation(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{"answer_id": answers[0].ID, "transcript_version": 1, "evaluator_version": "reference@1", "provider": "reference",
		"outcome": "evaluated", "output": evaluationOutput(id, answers[0])}
	if r := e.do(req{method: "POST", url: base + "/evaluations", headers: tier2, body: valid}); r.status != 201 || r.str("outcome") != "valid" {
		t.Fatalf("valid evaluation: %d %s", r.status, r.raw)
	}
	if r := e.do(req{method: "POST", url: base + "/evaluations", headers: tier2, body: valid}); r.status != 200 || r.str("outcome") != "duplicate" {
		t.Fatalf("redelivery: %d %s", r.status, r.raw)
	}
	for _, a := range answers[1:] {
		if r := e.do(req{method: "POST", url: base + "/evaluations", headers: tier2, body: abstain(a)}); r.status != 201 {
			t.Fatalf("abstention: %d %s", r.status, r.raw)
		}
	}
	if r := e.do(req{method: "POST", url: base + "/complete", headers: tier2, body: map[string]any{}}); r.status != 200 || r.str("state") != "HUMAN_REVIEW" || r.num("review_tasks") != 4 {
		t.Fatalf("complete: %d %s", r.status, r.raw)
	}

	reviewer, admin := e.staffToken("reviewer"), e.staffToken("admin")
	staff := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks", headers: staff(e.staff)}); r.status != 403 {
		t.Fatalf("a recruiter does not review: %d", r.status)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks", cookie: cookie}); r.status != 401 {
		t.Fatalf("a candidate cannot see review tasks: %d", r.status)
	}
	queue := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks", headers: staff(reviewer)})
	tasks, _ := queue.body["review_tasks"].([]any)
	if queue.status != 200 || len(tasks) != 4 {
		t.Fatalf("queue: %d %s", queue.status, queue.raw)
	}
	other := e.otherTenantStaff()
	for i, raw := range tasks {
		task := raw.(map[string]any)["task_id"].(string)
		if i == 0 {
			if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks/" + task, headers: map[string]string{"Authorization": "Bearer " + other}}); r.status != 403 && r.status != 404 {
				t.Fatalf("another tenant's task: %d", r.status)
			}
			detail := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks/" + task, headers: staff(reviewer)})
			if detail.status != 200 || !strings.Contains(string(detail.raw), "Each update writes a new row version.") || strings.Contains(string(detail.raw), "indicators") ||
				strings.Contains(string(detail.raw), "candidate_ref") || strings.Contains(string(detail.raw), "external_ref") {
				t.Fatalf("task detail: %d %s", detail.status, detail.raw)
			}
		}
		tok := reviewer
		if i == 3 {
			tok = admin
		}
		r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/review-tasks/" + task + "/outcome", headers: staff(tok), body: map[string]any{
			"scores": map[string]int{"technical_accuracy": 60, "architecture_maturity": 50, "edge_case_awareness": 40, "claim_support": 30}, "rationale": "Accurate but brief; no example."}})
		if r.status != 201 {
			t.Fatalf("outcome %d: %d %s", i, r.status, r.raw)
		}
		if want := map[bool]string{true: "REPORT_READY", false: "HUMAN_REVIEW"}[i == 3]; r.str("interview_state") != want {
			t.Fatalf("after outcome %d: %s", i, r.raw)
		}
	}
	report := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/report", headers: staff(e.staff)})
	if report.status != 200 || report.body["decision_support_only"] != true {
		t.Fatalf("report: %d %s", report.status, report.raw)
	}
	var parsed map[string]any
	_ = json.Unmarshal(report.raw, &parsed)
	if answers, _ := parsed["answers"].([]any); len(answers) != 4 {
		t.Fatalf("report answers: %s", report.raw)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/report", headers: map[string]string{"Authorization": "Bearer " + other}}); r.status != 404 {
		t.Fatalf("another tenant's report: %d", r.status)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/report", cookie: cookie}); r.status != 401 {
		t.Fatalf("a candidate cannot read the report: %d", r.status)
	}
	view := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie})
	if view.status != 200 || view.str("state") != "REPORT_READY" || strings.Contains(strings.ToLower(string(view.raw)), "score") {
		t.Fatalf("the candidate's view never shows scores: %d %s", view.status, view.raw)
	}
}
