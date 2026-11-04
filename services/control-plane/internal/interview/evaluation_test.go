package interview

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// completedAnswer is one finalized answer of a completed interview.
type completedAnswer struct {
	ID, Item, Segment, Text string
	StartMs, EndMs          int64
	Rubric                  string
}

var answerTexts = map[string]string{
	"q1":   "Every update writes a new version of the row and readers keep their snapshot.",
	"q1f1": "VACUUM removes the dead versions that updates leave behind.",
	"q2":   "I would store an idempotency key with each payment in one transaction.",
	"q3":   "I would look at the p99 dashboard first and check the latest deploy.",
}

// toCompletedNonSynthetic runs the plan to INTERVIEW_COMPLETED with candidate speech recorded as
// provider transcription (not synthetic: synthetic speech is never evaluated).
func (h *harness) toCompletedNonSynthetic() (string, []completedAnswer) {
	h.t.Helper()
	id, lease := h.toCalibration()
	h.calibrate(id, lease)
	var out []completedAnswer
	for range 4 {
		act, item := h.openActivation(id)
		h.clock.Advance(500 * time.Millisecond)
		_, err := h.svc.RenewLease(ctx, id, lease)
		h.must(err)
		h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
		h.clock.Advance(2 * time.Second)
		start := h.timeline(id)
		h.clock.Advance(3 * time.Second)
		end := h.timeline(id)
		key := h.nextKey()
		res, err := h.svc.IngestSegment(ctx, agent, lease, id, Submission{SegmentKey: key, Speaker: "candidate", Text: answerTexts[item],
			StartMs: start, EndMs: end, Source: "realtime_transcription", Engine: "openai_realtime"})
		h.must(err)
		h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, h.session(id).StateVersion, act, []string{key}, "agent_tool"))
		out = append(out, completedAnswer{Item: item, Segment: res.SegmentID, Text: answerTexts[item], StartMs: start, EndMs: end})
	}
	if s := h.session(id); s.State != flow.InterviewCompleted {
		h.t.Fatalf("expected INTERVIEW_COMPLETED, got %s", s.State)
	}
	for i := range out {
		h.must(h.db.CP.QueryRow(ctx, `SELECT a.id, q.rubric_id || '@' || q.rubric_version FROM ati.candidate_answers a
			JOIN ati.interview_questions q ON q.plan_id = 'plan_demo_backend_en' AND q.plan_version = 1 AND q.item_id = a.item_id
			WHERE a.session_id = $1 AND a.item_id = $2`, id, out[i].Item).Scan(&out[i].ID, &out[i].Rubric))
	}
	return id, out
}

// output is a valid evaluation of the answer with these scores (every non-zero dimension cites the
// answer's whole text as supporting evidence).
func output(sessionID string, a completedAnswer, scores map[string]int, confidence float64) json.RawMessage {
	weights := map[string]int{"technical_accuracy": 40, "architecture_maturity": 25, "edge_case_awareness": 20, "claim_support": 15}
	centi := 0
	var evidence []any
	for _, d := range []string{"technical_accuracy", "architecture_maturity", "edge_case_awareness", "claim_support"} {
		centi += scores[d] * weights[d]
		item := map[string]any{"dimension": d, "transcript_start_ms": a.StartMs, "transcript_end_ms": a.EndMs, "transcript_segment_ids": []string{a.Segment},
			"quote": a.Text, "claim": "claim", "assessment": "correct", "reference_ids": []string{}}
		if scores[d] == 0 {
			item["quote"], item["assessment"] = "", "not_addressed"
		}
		evidence = append(evidence, item)
	}
	b, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "question_id": a.Item, "evaluation_version": a.Rubric,
		"transcript_quality": map[string]any{"score": 0.9, "language": "en", "ambiguities": []string{}, "missing_segments": false},
		"scores":             scores, "weighted_score": float64(centi) / 100,
		"confidence": map[string]any{"score": confidence, "calibrated": false, "reason": "r"}, "evidence": evidence,
		"missing_concepts": []string{}, "contradictions": []string{}, "unsupported_claims": []string{},
		"hallucination_risk": map[string]any{"score": 0, "severity": "low", "evidence": []string{}}, "recommendation": "NEXT_QUESTION", "review_required": false,
	})
	return b
}

func uniform(v int) map[string]int {
	return map[string]int{"technical_accuracy": v, "architecture_maturity": v, "edge_case_awareness": v, "claim_support": v}
}

func submission(a completedAnswer, evaluator string, out json.RawMessage) EvaluationSubmission {
	return EvaluationSubmission{AnswerID: a.ID, TranscriptVersion: 1, EvaluatorVersion: evaluator, Provider: "reference", Outcome: "evaluated", Output: out}
}

func conflictCode(err error) string {
	var c *ConflictError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

func TestTier2HandoffReviewAndReport(t *testing.T) {
	h := newHarness(t)
	id, answers := h.toCompletedNonSynthetic()

	// INV-6: nothing is accepted before T14 moves the interview to EVALUATION_PENDING.
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[0], "reference@1", output(id, answers[0], uniform(80), 0.9))); conflictCode(err) != "evaluation_not_pending" {
		t.Fatalf("an evaluation before T14: %v", err)
	}
	h.must(h.svc.EnqueueEvaluation(ctx, id))
	h.must(h.svc.EnqueueEvaluation(ctx, id)) // the relay may publish twice
	if s := h.session(id); s.State != flow.EvaluationPending {
		t.Fatalf("T14: %s", s.State)
	}

	// q1: a valid evaluation, then the same delivery again.
	rec, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[0], "reference@1", output(id, answers[0], uniform(80), 0.9)))
	if err != nil || rec.Outcome != "valid" || rec.EvaluationID == nil {
		t.Fatalf("valid evaluation: %+v %v", rec, err)
	}
	again, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[0], "reference@1", output(id, answers[0], uniform(10), 0.2)))
	if err != nil || again.Outcome != "duplicate" || *again.EvaluationID != *rec.EvaluationID {
		t.Fatalf("a duplicate delivery must return the first evaluation: %+v %v", again, err)
	}
	if n := h.count(`SELECT count(*) FROM ati.evaluation_attempts WHERE answer_id = $1`, answers[0].ID); n != 1 {
		t.Fatalf("a duplicate must not add attempts: %d", n)
	}
	// A second, independent evaluator that disagrees.
	other, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[0], "openai:gpt-6.1-sol@p1", output(id, answers[0], uniform(40), 0.9)))
	if err != nil || other.Outcome != "valid" {
		t.Fatalf("second evaluator: %+v %v", other, err)
	}

	// q1f1: output quoting words the candidate never said is kept raw and rejected.
	bad := output(id, answers[1], uniform(70), 0.95)
	bad = json.RawMessage(strings.Replace(string(bad), answers[1].Text, "VACUUM is never needed in PostgreSQL.", 1))
	rec, err = h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[1], "reference@1", bad))
	if err != nil || rec.Outcome != "invalid" || rec.EvaluationID != nil || !slices.ContainsFunc(rec.Problems, func(p string) bool { return strings.Contains(p, "do not appear") }) {
		t.Fatalf("a fabricated quote must be rejected: %+v %v", rec, err)
	}
	var raw []byte
	h.must(h.db.CP.QueryRow(ctx, `SELECT raw_output FROM ati.evaluation_attempts WHERE id = $1`, rec.AttemptID).Scan(&raw))
	if !strings.Contains(string(raw), "VACUUM is never needed") {
		t.Fatal("the raw output is stored as received, separately from canonical evaluations")
	}

	// q2: the evaluator abstains; q3: nothing arrives. Wrong transcript versions are refused.
	reason := "transcript_quality"
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, EvaluationSubmission{AnswerID: answers[2].ID, TranscriptVersion: 1, EvaluatorVersion: "reference@1",
		Provider: "reference", Outcome: "abstained", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	stale := submission(answers[3], "reference@1", output(id, answers[3], uniform(50), 0.9))
	stale.TranscriptVersion = 2
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, stale); conflictCode(err) != "transcript_version_mismatch" {
		t.Fatalf("another transcript version: %v", err)
	}

	routing, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "")
	if err != nil || routing.State != flow.HumanReview || routing.ReviewTasks != 4 {
		t.Fatalf("T15: %+v %v", routing, err)
	}
	if again, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, ""); err != nil || again.ReviewTasks != 4 {
		t.Fatalf("completing twice changes nothing: %+v %v", again, err)
	}
	reasons := map[string][]string{}
	tasks := map[string]string{}
	rows, err := h.db.CP.Query(ctx, `SELECT t.id, a.item_id, t.reasons FROM ati.review_tasks t JOIN ati.candidate_answers a ON a.id = t.answer_id WHERE t.session_id = $1`, id)
	h.must(err)
	for rows.Next() {
		var task, item string
		var rs []string
		h.must(rows.Scan(&task, &item, &rs))
		reasons[item], tasks[item] = rs, task
	}
	rows.Close()
	for item, want := range map[string]string{"q1": "evaluator_disagreement", "q1f1": "evaluator_output_invalid", "q2": "evaluator_abstained", "q3": "not_evaluated"} {
		if !slices.Contains(reasons[item], want) || !slices.Contains(reasons[item], "policy_all_reviewed") {
			t.Errorf("%s review reasons %v lack %s", item, reasons[item], want)
		}
	}
	// No more evaluations once review started (T16 is never fired: no automatic report).
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, submission(answers[3], "reference@1", output(id, answers[3], uniform(50), 0.9))); conflictCode(err) != "evaluation_not_pending" {
		t.Fatalf("evaluation after review started: %v", err)
	}

	// Human review: tenant scoping, validation, then decisions on every answer.
	reviewer := Actor{Type: flow.ActorReviewer, ID: staffRow(h, "reviewer")}
	decision := ReviewDecision{Scores: map[string]int{"technical_accuracy": 70, "architecture_maturity": 60, "edge_case_awareness": 50, "claim_support": 40}, Rationale: "Covers row versions; no example."}
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, ids.New(ids.Tenant), reviewer.ID, tasks["q1"], decision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant's task: %v", err)
	}
	var invalid *ValidationError
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q1"], ReviewDecision{Scores: map[string]int{"technical_accuracy": 70}, Rationale: decision.Rationale}); !errors.As(err, &invalid) {
		t.Fatalf("partial scores: %v", err)
	}
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q1"], ReviewDecision{Scores: decision.Scores, Rationale: "ok"}); !errors.As(err, &invalid) {
		t.Fatalf("a rationale is required: %v", err)
	}
	if _, err := h.svc.Report(ctx, h.tenant, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no report before review completes: %v", err)
	}
	for i, item := range []string{"q1", "q1f1", "q2", "q3"} {
		res, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks[item], decision)
		h.must(err)
		if (i < 3) != (res.InterviewState == flow.HumanReview) || res.OpenTasks != 3-i {
			t.Fatalf("after %s: %+v", item, res)
		}
	}
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q1"], decision); conflictCode(err) != "review_not_open" {
		t.Fatalf("a second decision: %v", err)
	}
	if s := h.session(id); s.State != flow.ReportReady {
		t.Fatalf("T17: %s", s.State)
	}

	// Override audit integrity: reviewer, prior and revised scores, rationale, rubric version.
	var prior, revised map[string]int
	var priorCenti, centi int
	var rubricRef, rationale, reviewerID string
	h.must(h.db.CP.QueryRow(ctx, `SELECT prior_scores, prior_weighted_score_centi, scores, weighted_score_centi, rubric_id || '@' || rubric_version, rationale, reviewer_id
		FROM ati.review_outcomes WHERE task_id = $1`, tasks["q1"]).Scan(&prior, &priorCenti, &revised, &centi, &rubricRef, &rationale, &reviewerID))
	if prior["technical_accuracy"] != 80 || priorCenti != 8000 || centi != 4000*0+70*40+60*25+50*20+40*15 || rubricRef != answers[0].Rubric || reviewerID != reviewer.ID || rationale != decision.Rationale {
		t.Fatalf("override record: prior=%v/%d revised=%v/%d rubric=%s reviewer=%s", prior, priorCenti, revised, centi, rubricRef, reviewerID)
	}
	if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'review.outcome' AND details::text LIKE '%' || $2 || '%'`, id, "Covers row versions"); n != 0 {
		t.Fatal("the rationale is not copied into audit logs")
	}

	body, err := h.svc.Report(ctx, h.tenant, id)
	h.must(err)
	var report map[string]any
	h.must(json.Unmarshal(body, &report))
	if report["decision_support_only"] != true || len(report["answers"].([]any)) != 4 {
		t.Fatalf("report: %s", body)
	}
	// No field may carry a decision, recommendation, ranking or the candidate's identity.
	for _, key := range jsonKeys(report) {
		k := strings.ToLower(key)
		for _, forbidden := range []string{"hire", "hiring", "reject", "rank", "recommend", "candidate", "external_ref"} {
			if strings.Contains(k, forbidden) {
				t.Fatalf("report field %q", key)
			}
		}
		if strings.Contains(k, "decision") && k != "decision_support_only" {
			t.Fatalf("report field %q", key)
		}
	}
	if _, err := h.svc.Report(ctx, ids.New(ids.Tenant), id); !errors.Is(err, ErrNotFound) {
		t.Fatal("another tenant cannot read the report")
	}
}

// A pipeline that gives up (for example after dead-lettering) still hands every answer to a reviewer.
func TestPipelineFailureFallsBackToHumanReview(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCompletedNonSynthetic()
	h.must(h.svc.EnqueueEvaluation(ctx, id))
	if _, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "Bad Reason!"); err == nil {
		t.Fatal("a failure reason is a short machine reason")
	}
	routing, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "dead_lettered")
	if err != nil || routing.State != flow.HumanReview || routing.ReviewTasks != 4 {
		t.Fatalf("fallback: %+v %v", routing, err)
	}
	if n := h.count(`SELECT count(*) FROM ati.review_tasks WHERE session_id = $1 AND 'pipeline_failed' = ANY(reasons) AND 'not_evaluated' = ANY(reasons)`, id); n != 4 {
		t.Fatalf("every task carries the failure: %d", n)
	}
}

// Synthetic test speech is never evaluated (the scripted engine's transcripts): only an abstention
// is accepted, and the reviewer is told why.
func TestSyntheticSpeechIsNeverEvaluated(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.calibrate(id, lease)
	for range 4 {
		h.answerOpenQuestion(id, lease, "synthetic answer")
	}
	h.must(h.svc.EnqueueEvaluation(ctx, id))
	var answerID string
	h.must(h.db.CP.QueryRow(ctx, `SELECT id FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1'`, id).Scan(&answerID))
	sub := EvaluationSubmission{AnswerID: answerID, TranscriptVersion: 1, EvaluatorVersion: "reference@1", Provider: "reference", Outcome: "evaluated", Output: json.RawMessage(`{}`)}
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, sub); conflictCode(err) != "synthetic_source" {
		t.Fatalf("synthetic speech evaluated: %v", err)
	}
	reason := "synthetic_source"
	sub.Outcome, sub.Output, sub.Reason = "abstained", nil, &reason
	if _, err := h.svc.RecordEvaluation(ctx, Tier2Actor, id, sub); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "")
	h.must(err)
	if n := h.count(`SELECT count(*) FROM ati.review_tasks WHERE session_id = $1 AND 'synthetic_source' = ANY(reasons)`, id); n != 4 {
		t.Fatalf("reviewers are told the speech is synthetic: %d", n)
	}
}

// calibrate: the candidate answers the audio check, then the agent confirms calibration.
func (h *harness) calibrate(id string, lease LeaseRef) {
	h.t.Helper()
	h.clock.Advance(time.Second)
	_, _, err := h.segment(id, lease, "candidate", 200, 900, "yes I can hear you")
	h.must(err)
	h.must(h.svc.CalibrationComplete(ctx, agent, &lease, id, h.session(id).StateVersion))
}

func jsonKeys(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			out = append(out, k)
			out = append(out, jsonKeys(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, jsonKeys(child)...)
		}
	}
	return out
}

func staffRow(h *harness, role string) string {
	h.t.Helper()
	id := ids.New(ids.Staff)
	_, err := h.db.Owner.Exec(ctx, `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,$4,'t',now() + interval '1 day')`,
		id, h.tenant, ids.Hash(fmt.Sprint(time.Now().UnixNano(), role)), role)
	h.must(err)
	return id
}

// A question without a rubric cannot be scored, but its answer is still reviewed: the reviewer records
// a rationale only, and the interview can still reach REPORT_READY.
func TestAnswerWithoutRubricIsReviewedWithARationaleOnly(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCompletedNonSynthetic()
	_, err := h.db.Owner.Exec(ctx, `ALTER TABLE ati.interview_questions DISABLE TRIGGER interview_questions_immutable;
		UPDATE ati.interview_questions SET rubric_id = NULL, rubric_version = NULL WHERE plan_id = 'plan_demo_backend_en' AND plan_version = 1 AND item_id = 'q3';
		ALTER TABLE ati.interview_questions ENABLE TRIGGER interview_questions_immutable`)
	h.must(err)
	h.must(h.svc.EnqueueEvaluation(ctx, id))
	_, err = h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "")
	h.must(err)
	tasks := map[string]string{}
	rows, err := h.db.CP.Query(ctx, `SELECT t.id, a.item_id FROM ati.review_tasks t JOIN ati.candidate_answers a ON a.id = t.answer_id WHERE t.session_id = $1`, id)
	h.must(err)
	for rows.Next() {
		var task, item string
		h.must(rows.Scan(&task, &item))
		tasks[item] = task
	}
	rows.Close()
	if n := h.count(`SELECT count(*) FROM ati.review_tasks WHERE id = $1 AND 'no_rubric' = ANY(reasons)`, tasks["q3"]); n != 1 {
		t.Fatal("the reviewer is told the question has no rubric")
	}
	reviewer := Actor{Type: flow.ActorReviewer, ID: staffRow(h, "reviewer")}
	scored := ReviewDecision{Scores: uniform(60), Rationale: "Reasonable coverage of the main points."}
	var invalid *ValidationError
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q3"], scored); !errors.As(err, &invalid) {
		t.Fatalf("scores without a rubric: %v", err)
	}
	if _, err := h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q1"], ReviewDecision{Rationale: scored.Rationale}); !errors.As(err, &invalid) {
		t.Fatalf("a rubric's answer needs scores: %v", err)
	}
	h.mustOutcome(h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks["q3"], ReviewDecision{Rationale: "No rubric; reviewed for the record only."}))
	detail, err := h.svc.ReviewTask(ctx, h.tenant, tasks["q3"])
	h.must(err)
	if detail.Outcome == nil || detail.Outcome.Scores != nil || detail.Outcome.WeightedScore != nil || detail.Rubric != nil {
		t.Fatalf("rationale-only outcome: %+v", detail.Outcome)
	}
	for _, item := range []string{"q1", "q1f1", "q2"} {
		h.mustOutcome(h.svc.SubmitReviewOutcome(ctx, reviewer, h.tenant, reviewer.ID, tasks[item], scored))
	}
	if s := h.session(id); s.State != flow.ReportReady {
		t.Fatalf("T17: %s", s.State)
	}
	body, err := h.svc.Report(ctx, h.tenant, id)
	h.must(err)
	var report struct {
		Answers []struct {
			ItemID          string  `json:"item_id"`
			Rubric          *string `json:"rubric"`
			RubricSynthetic *bool   `json:"rubric_synthetic"`
			Reviewed        struct {
				Scores        map[string]int `json:"scores"`
				WeightedScore *float64       `json:"weighted_score"`
				Rationale     string         `json:"rationale"`
			} `json:"reviewed"`
		} `json:"answers"`
	}
	h.must(json.Unmarshal(body, &report))
	for _, a := range report.Answers {
		unscored := a.Rubric == nil && a.RubricSynthetic == nil && a.Reviewed.Scores == nil && a.Reviewed.WeightedScore == nil
		if (a.ItemID == "q3") != unscored || a.Reviewed.Rationale == "" {
			t.Fatalf("report answer %s: %s", a.ItemID, body)
		}
		if a.ItemID != "q3" && (*a.Reviewed.WeightedScore != 60 || *a.Rubric == "" || !*a.RubricSynthetic) {
			t.Fatalf("report answer %s: %s", a.ItemID, body)
		}
	}
}

func (h *harness) mustOutcome(_ *ReviewOutcomeResult, err error) {
	h.t.Helper()
	h.must(err)
}
