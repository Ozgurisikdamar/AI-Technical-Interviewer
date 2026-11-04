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

// inReview brings a completed interview to HUMAN_REVIEW: the first answer has a valid AI
// evaluation, the evaluator abstained on the others.
func (e *env) inReview() (id string, answers []storedAnswer) {
	e.t.Helper()
	id, _ = e.completeInterview()
	base := e.internal.URL + "/internal/v1/tier2/sessions/" + id
	tier2 := map[string]string{"Authorization": "Bearer " + tier2Token}
	if err := e.svc.EnqueueEvaluation(context.Background(), id); err != nil {
		e.t.Fatal(err)
	}
	answers = e.answers(id)
	for i, a := range answers {
		body := map[string]any{"answer_id": a.ID, "transcript_version": 1, "evaluator_version": "reference@1", "provider": "reference", "outcome": "abstained", "reason": "transcript_quality"}
		if i == 0 {
			body = map[string]any{"answer_id": a.ID, "transcript_version": 1, "evaluator_version": "reference@1", "provider": "reference", "outcome": "evaluated", "output": evaluationOutput(id, a)}
		}
		if r := e.do(req{method: "POST", url: base + "/evaluations", headers: tier2, body: body}); r.status != 201 {
			e.t.Fatalf("evaluation %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := e.do(req{method: "POST", url: base + "/complete", headers: tier2, body: map[string]any{}}); r.status != 200 || r.str("state") != "HUMAN_REVIEW" {
		e.t.Fatalf("complete: %d %s", r.status, r.raw)
	}
	return id, answers
}

func TestReviewInTheBrowserSessionAndTheReportList(t *testing.T) {
	e := newEnv(t)
	first, answers := e.inReview()
	second, _ := e.inReview()
	reviewer := staffCookie(t, e.signIn(e.staffToken("reviewer"), nil)).Value
	recruiter := staffCookie(t, e.signIn(e.staff, nil)).Value
	queue := func(query string) []map[string]any {
		r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks" + query, headers: asSession(reviewer)})
		if r.status != 200 {
			t.Fatalf("queue %s: %d %s", query, r.status, r.raw)
		}
		var out []map[string]any
		for _, raw := range r.body["review_tasks"].([]any) {
			out = append(out, raw.(map[string]any))
		}
		return out
	}
	if all := queue(""); len(all) != 8 {
		t.Fatalf("both interviews are queued: %d", len(all))
	}
	tasks := queue("?interview_id=" + first)
	if len(tasks) != 4 {
		t.Fatalf("one interview's tasks: %d", len(tasks))
	}
	for i, task := range tasks {
		// Tasks created together keep the answers' order.
		if task["interview_id"] != first || task["answer_id"] != answers[i].ID {
			t.Fatalf("task %d: %v", i, task)
		}
	}
	for _, bad := range []string{"?interview_id=ses_short", "?interview_id=x%27%20OR%201=1", "?limit=0", "?status=all"} {
		if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks" + bad, headers: asSession(reviewer)}); r.status != 400 {
			t.Errorf("%s: %d", bad, r.status)
		}
	}
	decide := func(task string, headers map[string]string, viewed bool) response {
		return e.do(req{method: "POST", url: e.public.URL + "/api/v1/review-tasks/" + task + "/outcome", headers: headers, body: map[string]any{
			"scores":    map[string]int{"technical_accuracy": 60, "architecture_maturity": 50, "edge_case_awareness": 40, "claim_support": 30},
			"rationale": "Accurate but brief; no example.", "ai_draft_viewed": viewed}})
	}
	// A cookie-authenticated decision needs the console's origin (CSRF).
	noOrigin := map[string]string{"Cookie": api.StaffCookie + "=" + reviewer}
	if r := decide(tasks[0]["task_id"].(string), noOrigin, true); r.status != 403 || r.str("code") != "origin_mismatch" {
		t.Fatalf("decision without an Origin: %d %s", r.status, r.raw)
	}
	for i, task := range tasks {
		if r := decide(task["task_id"].(string), asSession(reviewer), true); r.status != 201 {
			t.Fatalf("decision %d: %d %s", i, r.status, r.raw)
		}
		if i == 0 {
			if r := decide(task["task_id"].(string), asSession(reviewer), true); r.status != 409 || r.str("code") != "review_already_completed" {
				t.Fatalf("a second decision on one answer: %d %s", r.status, r.raw)
			}
		}
	}
	if r := decide(tasks[0]["task_id"].(string), asSession(reviewer), true); r.status != 409 || r.str("code") != "review_not_open" {
		t.Fatalf("a decision once the report exists: %d %s", r.status, r.raw)
	}
	// Only the first answer had an AI draft to view; the claim is not recorded for the others.
	if n := e.count(`SELECT count(*) FROM ati.review_outcomes WHERE session_id = $1 AND ai_draft_viewed = (answer_id = $2)`, first, answers[0].ID); n != 4 {
		t.Fatalf("ai_draft_viewed is recorded only where a draft existed: %d of 4", n)
	}
	detail := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks/" + tasks[0]["task_id"].(string), headers: asSession(reviewer)})
	if detail.status != 200 || detail.body["outcome"].(map[string]any)["ai_draft_viewed"] != true {
		t.Fatalf("the recorded outcome: %d %s", detail.status, detail.raw)
	}
	list := e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports", headers: asSession(recruiter)})
	reports, _ := list.body["reports"].([]any)
	if list.status != 200 || len(reports) != 1 {
		t.Fatalf("reports: %d %s", list.status, list.raw)
	}
	entry := reports[0].(map[string]any)
	if entry["interview_id"] != first || entry["answer_count"] != float64(4) || entry["language"] != "en" || entry["plan"].(map[string]any)["plan_id"] != "plan_demo_backend_en" {
		t.Fatalf("report entry: %v", entry)
	}
	for _, leak := range []string{"score", "candidate", "ref_", "rationale", "Each update"} {
		if strings.Contains(string(list.raw), leak) {
			t.Fatalf("the report list holds %q: %s", leak, list.raw)
		}
	}
	report := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + first + "/report", headers: asSession(recruiter)})
	var parsed struct {
		Answers []struct {
			Reviewed struct {
				AIDraftViewed bool `json:"ai_draft_viewed"`
			} `json:"reviewed"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(report.raw, &parsed); report.status != 200 || err != nil || len(parsed.Answers) != 4 || !parsed.Answers[0].Reviewed.AIDraftViewed || parsed.Answers[1].Reviewed.AIDraftViewed {
		t.Fatalf("report through the session: %d %s", report.status, report.raw)
	}
	other := e.otherTenantStaff()
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports", headers: map[string]string{"Authorization": "Bearer " + other}}); r.status != 200 || strings.Contains(string(r.raw), first) {
		t.Fatalf("another tenant's report list: %d %s", r.status, r.raw)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports?limit=201", headers: asSession(recruiter)}); r.status != 400 {
		t.Fatalf("limit above 200: %d", r.status)
	}
	if left := queue("?interview_id=" + second); len(left) != 4 {
		t.Fatalf("the other interview is still queued: %d", len(left))
	}
}
