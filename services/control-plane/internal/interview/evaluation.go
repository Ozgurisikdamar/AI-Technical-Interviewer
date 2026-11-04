package interview

// Tier-2 handoff, human review and reports (ADR-022). Evaluator output reaches the database only
// through RecordEvaluation, after internal/evaluation has validated it against the stored
// transcript. Every answer is routed to human review (T15); a report exists only after the last
// review (T17). No operation fires T16 (an automatic report).

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/evaluation"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// Tier2Actor is the Tier-2 pipeline acting through the internal API (ADR-015).
var Tier2Actor = Actor{Type: flow.ActorTier2, ID: "tier2-evaluator"}

// MaxEvaluatorOutput bounds a stored evaluator response.
const MaxEvaluatorOutput = 256 << 10

var (
	reasonPattern    = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	evaluatorPattern = regexp.MustCompile(`^[A-Za-z0-9_.:@/-]{1,200}$`)
)

// EnqueueEvaluation applies T14 (INTERVIEW_COMPLETED -> EVALUATION_PENDING) for the outbox relay,
// just before it publishes the session's interview.completed event. Repeating it is a no-op.
func (s *Service) EnqueueEvaluation(ctx context.Context, sessionID string) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		switch sess.State {
		case flow.EvaluationPending, flow.HumanReview, flow.ReportReady:
			return nil
		}
		oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem, ID: "outbox-relay"}, after)
		if err != nil {
			return err
		}
		oc.cause = "evaluation_enqueued"
		return s.fire(ctx, oc, flow.EvEvaluationEnqueued)
	})
}

// EvaluationSubmission is one evaluator attempt reported by the Tier-2 worker.
type EvaluationSubmission struct {
	AnswerID          string          `json:"answer_id"`
	TranscriptVersion int             `json:"transcript_version"`
	EvaluatorVersion  string          `json:"evaluator_version"`
	Provider          string          `json:"provider"`
	ModelID           *string         `json:"model_id"`
	PromptSHA256      *string         `json:"prompt_sha256"`
	ProviderRequestID *string         `json:"provider_request_id"`
	Outcome           string          `json:"outcome"` // evaluated | abstained | failed
	Output            json.RawMessage `json:"output"`  // evaluated: the evaluator's JSON as received
	Reason            *string         `json:"reason"`  // abstained or failed: a short machine reason
	LatencyMs         *int            `json:"latency_ms"`
	InputTokens       *int            `json:"input_tokens"`
	OutputTokens      *int            `json:"output_tokens"`
}

// RecordedEvaluation is the outcome of recording an attempt.
type RecordedEvaluation struct {
	AttemptID    string   `json:"attempt_id"`
	EvaluationID *string  `json:"evaluation_id"`
	Outcome      string   `json:"outcome"` // valid | invalid | abstained | failed | duplicate
	Problems     []string `json:"problems"`
}

func (sub *EvaluationSubmission) check() error {
	switch {
	case !regexp.MustCompile(`^ans_[A-Za-z0-9]{16,}$`).MatchString(sub.AnswerID):
		return &ValidationError{"answer_id is malformed"}
	case sub.TranscriptVersion < 1:
		return &ValidationError{"transcript_version must be at least 1"}
	case !evaluatorPattern.MatchString(sub.EvaluatorVersion):
		return &ValidationError{"evaluator_version is malformed"}
	case !slices.Contains([]string{"reference", "openai", "anthropic"}, sub.Provider):
		return &ValidationError{"provider must be reference, openai or anthropic"}
	}
	switch sub.Outcome {
	case "evaluated":
		if len(sub.Output) == 0 || string(sub.Output) == "null" {
			return &ValidationError{"an evaluated attempt carries the evaluator output"}
		}
		if len(sub.Output) > MaxEvaluatorOutput {
			return &ValidationError{"evaluator output is too large"}
		}
	case "abstained", "failed":
		if sub.Reason == nil || !reasonPattern.MatchString(*sub.Reason) {
			return &ValidationError{"an abstained or failed attempt carries a short machine reason"}
		}
	default:
		return &ValidationError{"outcome must be evaluated, abstained or failed"}
	}
	return nil
}

type answerRow struct {
	ID                string
	ItemID            string
	TranscriptVersion int
	SegmentIDs        []string
	MissingSegments   bool
	IntegrityFlags    []string
	RubricID          *string
	RubricVersion     *int
	Question          string
}

const answerColumns = `a.id, a.item_id, a.transcript_version, a.segment_ids, a.missing_segments, a.integrity_flags, q.rubric_id, q.rubric_version, q.text`

func scanAnswer(row pgx.Row) (*answerRow, error) {
	var a answerRow
	if err := row.Scan(&a.ID, &a.ItemID, &a.TranscriptVersion, &a.SegmentIDs, &a.MissingSegments, &a.IntegrityFlags, &a.RubricID, &a.RubricVersion, &a.Question); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (s *Service) loadAnswer(ctx context.Context, tx pgx.Tx, sess *sessionRow, answerID string) (*answerRow, error) {
	return scanAnswer(tx.QueryRow(ctx, `SELECT `+answerColumns+` FROM ati.candidate_answers a
		JOIN ati.interview_questions q ON q.plan_id = $2 AND q.plan_version = $3 AND q.item_id = a.item_id
		WHERE a.id = $1 AND a.session_id = $4`, answerID, sess.PlanID, sess.PlanVersion, sess.ID))
}

func (s *Service) loadRubric(ctx context.Context, tx pgx.Tx, id string, version int) (*evaluation.Rubric, json.RawMessage, error) {
	var body json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT body FROM ati.rubrics WHERE rubric_id = $1 AND version = $2`, id, version).Scan(&body); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, &ConflictError{Code: "rubric_unavailable", Msg: "the answer's rubric is not stored"}
		}
		return nil, nil, err
	}
	r, err := evaluation.ParseRubric(body)
	return r, body, err
}

func (s *Service) answerSegments(ctx context.Context, tx pgx.Tx, sessionID string, a *answerRow) ([]evaluation.Segment, error) {
	rows, err := tx.Query(ctx, `SELECT id, speaker, start_ms, end_ms, text FROM ati.transcript_segments
		WHERE session_id = $1 AND id = ANY($2) AND transcript_version = $3 AND evidence_eligible ORDER BY start_ms, sequence`,
		sessionID, a.SegmentIDs, a.TranscriptVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evaluation.Segment
	for rows.Next() {
		var seg evaluation.Segment
		if err := rows.Scan(&seg.ID, &seg.Speaker, &seg.StartMs, &seg.EndMs, &seg.Text); err != nil {
			return nil, err
		}
		out = append(out, seg)
	}
	return out, rows.Err()
}

// assessmentRetention is when stored evaluator output and evaluations may be purged: the
// session's assessment-output retention policy, fixed when the interview was created.
func assessmentRetention(sess *sessionRow, now time.Time) time.Time {
	var r struct {
		AssessmentOutputDays int `json:"assessment_output_days"`
	}
	_ = json.Unmarshal(sess.Retention, &r)
	return now.AddDate(0, 0, r.AssessmentOutputDays)
}

// RecordEvaluation stores one evaluator attempt exactly as received and, when the output passes
// validation, the canonical evaluation. Accepted only while the interview's evaluation is pending
// (INV-6: Tier-2 never acts on a live interview). Idempotent per answer, transcript version,
// rubric version and evaluator version (INV-2).
func (s *Service) RecordEvaluation(ctx context.Context, actor Actor, sessionID string, sub EvaluationSubmission) (*RecordedEvaluation, error) {
	if err := sub.check(); err != nil {
		return nil, err
	}
	out := &RecordedEvaluation{Problems: []string{}}
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if sess.State != flow.EvaluationPending {
			return &ConflictError{Code: "evaluation_not_pending", Msg: "evaluations are accepted only while the interview's evaluation is pending"}
		}
		a, err := s.loadAnswer(ctx, tx, sess, sub.AnswerID)
		if err != nil {
			return err
		}
		if a.RubricID == nil || a.RubricVersion == nil {
			return &ConflictError{Code: "no_rubric", Msg: "the answer's question has no rubric to evaluate against"}
		}
		if a.TranscriptVersion != sub.TranscriptVersion {
			return &ConflictError{Code: "transcript_version_mismatch", Msg: "the evaluation is for another transcript version"}
		}
		if sub.Outcome == "evaluated" && slices.Contains(a.IntegrityFlags, "synthetic_source") {
			return &ConflictError{Code: "synthetic_source", Msg: "synthetic test speech is never evaluated; the evaluator must abstain"}
		}
		var evalID, attemptID string
		err = tx.QueryRow(ctx, `SELECT id, attempt_id FROM ati.evaluations WHERE answer_id = $1 AND transcript_version = $2 AND rubric_id = $3
			AND rubric_version = $4 AND evaluator_version = $5`, a.ID, a.TranscriptVersion, *a.RubricID, *a.RubricVersion, sub.EvaluatorVersion).Scan(&evalID, &attemptID)
		if err == nil {
			out.AttemptID, out.EvaluationID, out.Outcome = attemptID, &evalID, "duplicate"
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		rubric, _, err := s.loadRubric(ctx, tx, *a.RubricID, *a.RubricVersion)
		if err != nil {
			return err
		}
		segments, err := s.answerSegments(ctx, tx, sess.ID, a)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		var res *evaluation.Result
		switch sub.Outcome {
		case "evaluated":
			res = evaluation.Validate(sub.Output, evaluation.Answer{SessionID: sess.ID, ItemID: a.ItemID, TranscriptVersion: a.TranscriptVersion,
				Segments: segments, MissingSegments: a.MissingSegments, IntegrityFlags: a.IntegrityFlags}, rubric)
			out.Outcome, out.Problems = "invalid", res.Problems
			if res.Valid {
				out.Outcome, out.Problems = "valid", []string{}
			}
		default:
			out.Outcome, out.Problems = sub.Outcome, []string{*sub.Reason}
		}
		var raw any
		if sub.Outcome == "evaluated" {
			raw = []byte(sub.Output)
		}
		out.AttemptID = ids.New(ids.EvaluationAttempt)
		if _, err := tx.Exec(ctx, `INSERT INTO ati.evaluation_attempts (id, tenant_id, session_id, answer_id, transcript_version, rubric_id, rubric_version,
			evaluator_version, provider, model_id, prompt_sha256, provider_request_id, outcome, problems, raw_output, latency_ms, input_tokens, output_tokens,
			retention_until, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			out.AttemptID, sess.TenantID, sess.ID, a.ID, a.TranscriptVersion, rubric.ID, rubric.Version, sub.EvaluatorVersion, sub.Provider,
			sub.ModelID, sub.PromptSHA256, sub.ProviderRequestID, out.Outcome, out.Problems, raw, sub.LatencyMs, sub.InputTokens, sub.OutputTokens,
			assessmentRetention(sess, now), now); err != nil {
			return err
		}
		if out.Outcome == "valid" {
			reasons := res.RoutingReasons
			others, err := tx.Query(ctx, `SELECT scores FROM ati.evaluations WHERE answer_id = $1 AND transcript_version = $2 AND rubric_id = $3
				AND rubric_version = $4 AND evaluator_version <> $5`, a.ID, a.TranscriptVersion, rubric.ID, rubric.Version, sub.EvaluatorVersion)
			if err != nil {
				return err
			}
			for others.Next() {
				var scores map[string]int
				if err := others.Scan(&scores); err != nil {
					others.Close()
					return err
				}
				if evaluation.Disagree(res.Scores, scores) && !slices.Contains(reasons, "evaluator_disagreement") {
					reasons = append(reasons, "evaluator_disagreement")
				}
			}
			others.Close()
			if err := others.Err(); err != nil {
				return err
			}
			id := ids.New(ids.Evaluation)
			out.EvaluationID = &id
			evidence, _ := json.Marshal(res.Evidence)
			scores, _ := json.Marshal(res.Scores)
			if _, err := tx.Exec(ctx, `INSERT INTO ati.evaluations (id, attempt_id, tenant_id, session_id, answer_id, transcript_version, rubric_id, rubric_version,
				evaluator_version, scores, weighted_score_centi, confidence, evidence, routing_reasons, recommendation, output_risk, retention_until, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				id, out.AttemptID, sess.TenantID, sess.ID, a.ID, a.TranscriptVersion, rubric.ID, rubric.Version, sub.EvaluatorVersion, scores,
				res.WeightedScoreCenti, res.Confidence, evidence, reasons, res.Recommendation, res.OutputRisk, assessmentRetention(sess, now), now); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, sess.TenantID, actor, "evaluation.recorded", "interview_session", sess.ID, "allowed", map[string]any{
			"answer_id": a.ID, "attempt_id": out.AttemptID, "evaluator_version": sub.EvaluatorVersion, "outcome": out.Outcome, "problems": len(out.Problems),
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReviewRouting summarizes the review tasks of an interview.
type ReviewRouting struct {
	State       flow.State `json:"state"`
	ReviewTasks int        `json:"review_tasks"`
}

// CompleteEvaluation routes the interview to human review (T15): every answer gets one review task
// with the reasons a reviewer should know, whether or not a valid evaluation exists. failure records
// that the pipeline gave up on the interview (for example once its message was dead-lettered);
// review is then the fallback. Repeating it changes nothing.
func (s *Service) CompleteEvaluation(ctx context.Context, actor Actor, sessionID, failure string) (*ReviewRouting, error) {
	if failure != "" && !reasonPattern.MatchString(failure) {
		return nil, &ValidationError{"failure must be a short machine reason"}
	}
	out := &ReviewRouting{}
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if sess.State == flow.HumanReview || sess.State == flow.ReportReady {
			out.State = sess.State
			return tx.QueryRow(ctx, `SELECT count(*) FROM ati.review_tasks WHERE session_id = $1`, sess.ID).Scan(&out.ReviewTasks)
		}
		if sess.State != flow.EvaluationPending {
			return &ConflictError{Code: "evaluation_not_pending", Msg: "the interview's evaluation is not pending"}
		}
		rows, err := tx.Query(ctx, `SELECT `+answerColumns+` FROM ati.candidate_answers a
			JOIN ati.interview_questions q ON q.plan_id = $2 AND q.plan_version = $3 AND q.item_id = a.item_id
			WHERE a.session_id = $1 ORDER BY a.finalized_at, a.id`, sess.ID, sess.PlanID, sess.PlanVersion)
		if err != nil {
			return err
		}
		var answers []*answerRow
		for rows.Next() {
			a, err := scanAnswer(rows)
			if err != nil {
				rows.Close()
				return err
			}
			answers = append(answers, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(answers) == 0 {
			return &ConflictError{Code: "no_answers", Msg: "the interview has no answers to review"}
		}
		for _, a := range answers {
			evalID, reasons, err := s.reviewReasons(ctx, tx, a, failure)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ati.review_tasks (id, tenant_id, session_id, answer_id, evaluation_id, reasons, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (answer_id) DO NOTHING`,
				ids.New(ids.ReviewTask), sess.TenantID, sess.ID, a.ID, evalID, reasons, s.now().UTC()); err != nil {
				return err
			}
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.cause = "review_required"
		if err := s.fire(ctx, oc, flow.EvReviewRequired); err != nil {
			return err
		}
		out.State, out.ReviewTasks = oc.sess.State, len(answers)
		details := map[string]any{"review_tasks": len(answers)}
		if failure != "" {
			details["failure"] = failure
		}
		return s.audit(ctx, tx, sess.TenantID, actor, "evaluation.completed", "interview_session", sess.ID, "allowed", details)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// reviewReasons picks the evaluation a reviewer starts from (the first valid one) and lists why the
// answer needs a look.
func (s *Service) reviewReasons(ctx context.Context, tx pgx.Tx, a *answerRow, failure string) (*string, []string, error) {
	reasons := []string{"policy_all_reviewed"}
	add := func(rs ...string) {
		for _, r := range rs {
			if !slices.Contains(reasons, r) {
				reasons = append(reasons, r)
			}
		}
	}
	if failure != "" {
		add("pipeline_failed")
	}
	if a.RubricID == nil {
		add("no_rubric")
	}
	if slices.Contains(a.IntegrityFlags, "synthetic_source") {
		add("synthetic_source")
	}
	var evalID string
	var evalReasons []string
	err := tx.QueryRow(ctx, `SELECT id, routing_reasons FROM ati.evaluations WHERE answer_id = $1 AND transcript_version = $2 ORDER BY seq LIMIT 1`,
		a.ID, a.TranscriptVersion).Scan(&evalID, &evalReasons)
	if err == nil {
		add(evalReasons...)
		var disagree bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.evaluations WHERE answer_id = $1 AND 'evaluator_disagreement' = ANY(routing_reasons))`, a.ID).Scan(&disagree); err != nil {
			return nil, nil, err
		}
		if disagree {
			add("evaluator_disagreement")
		}
		return &evalID, reasons, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	var outcome string
	var problems []string
	err = tx.QueryRow(ctx, `SELECT outcome, problems FROM ati.evaluation_attempts WHERE answer_id = $1 ORDER BY seq DESC LIMIT 1`, a.ID).Scan(&outcome, &problems)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		add("not_evaluated")
	case err != nil:
		return nil, nil, err
	case outcome == "invalid":
		add("evaluator_output_invalid")
	case outcome == "abstained":
		add("evaluator_abstained")
	default:
		add("evaluation_failed")
	}
	return nil, reasons, nil
}

// ReviewTaskSummary is one entry of the review queue.
type ReviewTaskSummary struct {
	TaskID      string     `json:"task_id"`
	InterviewID string     `json:"interview_id"`
	AnswerID    string     `json:"answer_id"`
	ItemID      string     `json:"item_id"`
	Status      string     `json:"status"`
	Reasons     []string   `json:"reasons"`
	HasAIDraft  bool       `json:"has_ai_evaluation"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// ReviewQueue lists the tenant's review tasks with the given status, oldest first.
func (s *Service) ReviewQueue(ctx context.Context, tenantID, status string, limit int) ([]ReviewTaskSummary, error) {
	if status != "open" && status != "completed" {
		return nil, &ValidationError{"status must be open or completed"}
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id, t.session_id, t.answer_id, a.item_id, t.status, t.reasons, t.evaluation_id IS NOT NULL, t.created_at, t.completed_at
		FROM ati.review_tasks t JOIN ati.candidate_answers a ON a.id = t.answer_id
		WHERE t.tenant_id = $1 AND t.status = $2 ORDER BY t.created_at, t.id LIMIT $3`, tenantID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewTaskSummary{}
	for rows.Next() {
		var r ReviewTaskSummary
		if err := rows.Scan(&r.TaskID, &r.InterviewID, &r.AnswerID, &r.ItemID, &r.Status, &r.Reasons, &r.HasAIDraft, &r.CreatedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TranscriptExcerpt is an answer segment shown to a reviewer.
type TranscriptExcerpt struct {
	SegmentID string `json:"segment_id"`
	StartMs   int64  `json:"start_ms"`
	EndMs     int64  `json:"end_ms"`
	Text      string `json:"text"`
}

// AIEvaluationView is the evaluator's validated draft, shown as a starting point only.
type AIEvaluationView struct {
	EvaluationID     string                    `json:"evaluation_id"`
	EvaluatorVersion string                    `json:"evaluator_version"`
	Scores           map[string]int            `json:"scores"`
	WeightedScore    float64                   `json:"weighted_score"`
	Confidence       float64                   `json:"confidence"`
	Calibrated       bool                      `json:"calibrated"`
	Recommendation   string                    `json:"advisory_next_step"`
	OutputRisk       string                    `json:"evaluator_output_risk"`
	RoutingReasons   []string                  `json:"routing_reasons"`
	Evidence         []evaluation.EvidenceItem `json:"evidence"`
}

// ReviewOutcomeView is a recorded review decision. Scores are null for an answer without a rubric.
type ReviewOutcomeView struct {
	OutcomeID     string         `json:"outcome_id"`
	ReviewerID    string         `json:"reviewer_id"`
	Scores        map[string]int `json:"scores"`
	WeightedScore *float64       `json:"weighted_score"`
	Rationale     string         `json:"rationale"`
	DecidedAt     time.Time      `json:"decided_at"`
}

// ReviewTaskDetail is everything a reviewer needs for one answer, and nothing about the candidate.
type ReviewTaskDetail struct {
	ReviewTaskSummary
	Language     string              `json:"language"`
	Question     string              `json:"question"`
	Rubric       json.RawMessage     `json:"rubric"`
	Transcript   []TranscriptExcerpt `json:"transcript"`
	AIEvaluation *AIEvaluationView   `json:"ai_evaluation"`
	Outcome      *ReviewOutcomeView  `json:"outcome"`
}

// ReviewTask returns one review task of the tenant.
func (s *Service) ReviewTask(ctx context.Context, tenantID, taskID string) (*ReviewTaskDetail, error) {
	var d *ReviewTaskDetail
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		var err error
		d, err = s.reviewTask(ctx, tx, tenantID, taskID)
		return err
	})
	return d, err
}

func (s *Service) reviewTask(ctx context.Context, tx pgx.Tx, tenantID, taskID string) (*ReviewTaskDetail, error) {
	d := &ReviewTaskDetail{}
	var evalID *string
	err := tx.QueryRow(ctx, `SELECT t.id, t.session_id, t.answer_id, t.status, t.reasons, t.evaluation_id, t.created_at, t.completed_at
		FROM ati.review_tasks t WHERE t.id = $1 AND t.tenant_id = $2`, taskID, tenantID).
		Scan(&d.TaskID, &d.InterviewID, &d.AnswerID, &d.Status, &d.Reasons, &evalID, &d.CreatedAt, &d.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess, err := s.getSession(ctx, tx, d.InterviewID)
	if err != nil {
		return nil, err
	}
	a, err := s.loadAnswer(ctx, tx, sess, d.AnswerID)
	if err != nil {
		return nil, err
	}
	d.ItemID, d.Question, d.Language, d.HasAIDraft = a.ItemID, a.Question, sess.NoticeLanguage, evalID != nil
	if a.RubricID != nil && a.RubricVersion != nil {
		if _, body, err := s.loadRubric(ctx, tx, *a.RubricID, *a.RubricVersion); err == nil {
			d.Rubric = reviewerRubric(body)
		}
	}
	segments, err := s.answerSegments(ctx, tx, sess.ID, a)
	if err != nil {
		return nil, err
	}
	d.Transcript = []TranscriptExcerpt{}
	for _, seg := range segments {
		d.Transcript = append(d.Transcript, TranscriptExcerpt{SegmentID: seg.ID, StartMs: seg.StartMs, EndMs: seg.EndMs, Text: seg.Text})
	}
	if evalID != nil {
		v := &AIEvaluationView{EvaluationID: *evalID}
		var centi int
		var evidence json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT evaluator_version, scores, weighted_score_centi, confidence, calibrated, recommendation, output_risk, routing_reasons, evidence
			FROM ati.evaluations WHERE id = $1`, *evalID).Scan(&v.EvaluatorVersion, &v.Scores, &centi, &v.Confidence, &v.Calibrated, &v.Recommendation,
			&v.OutputRisk, &v.RoutingReasons, &evidence); err != nil {
			return nil, err
		}
		v.WeightedScore = float64(centi) / 100
		if err := json.Unmarshal(evidence, &v.Evidence); err != nil {
			return nil, err
		}
		d.AIEvaluation = v
	}
	o := &ReviewOutcomeView{}
	var centi *int
	err = tx.QueryRow(ctx, `SELECT id, reviewer_id, scores, weighted_score_centi, rationale, decided_at FROM ati.review_outcomes WHERE task_id = $1`, d.TaskID).
		Scan(&o.OutcomeID, &o.ReviewerID, &o.Scores, &centi, &o.Rationale, &o.DecidedAt)
	switch {
	case err == nil:
		o.WeightedScore = centiPoints(centi)
		d.Outcome = o
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	return d, nil
}

// reviewerRubric is the rubric as a reviewer sees it: criteria and references, without the
// synthetic indicators the deterministic reference evaluator uses.
func reviewerRubric(body json.RawMessage) json.RawMessage {
	var r map[string]any
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	if criteria, ok := r["criteria"].([]any); ok {
		for _, c := range criteria {
			if m, ok := c.(map[string]any); ok {
				delete(m, "indicators")
			}
		}
	}
	b, _ := json.Marshal(r)
	return b
}

// ReviewDecision is a reviewer's scores and rationale for one answer. An answer whose question has no
// rubric is reviewed with a rationale only: it cannot be scored.
type ReviewDecision struct {
	Scores    map[string]int `json:"scores"`
	Rationale string         `json:"rationale"`
}

// ReviewOutcomeResult is the outcome of a review decision.
type ReviewOutcomeResult struct {
	OutcomeID      string     `json:"outcome_id"`
	InterviewState flow.State `json:"interview_state"`
	OpenTasks      int        `json:"open_review_tasks"`
}

// SubmitReviewOutcome records a reviewer's decision on one answer (PROMPT_AND_EVALUATION section 6:
// reviewer, prior and revised scores, rationale, time and rubric version). When it decides the last
// open task of the interview, the report is written and T17 moves the interview to REPORT_READY.
func (s *Service) SubmitReviewOutcome(ctx context.Context, actor Actor, tenantID, reviewerID, taskID string, dec ReviewDecision) (*ReviewOutcomeResult, error) {
	if n := len([]rune(dec.Rationale)); n < 10 || n > 4000 {
		return nil, &ValidationError{"rationale must be 10 to 4000 characters"}
	}
	out := &ReviewOutcomeResult{}
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		var sessionID string
		if err := tx.QueryRow(ctx, `SELECT session_id FROM ati.review_tasks WHERE id = $1 AND tenant_id = $2`, taskID, tenantID).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		sess, err := s.lockSession(ctx, tx, sessionID) // session first, then its task (lock order)
		if err != nil {
			return err
		}
		if sess.State != flow.HumanReview {
			return &ConflictError{Code: "review_not_open", Msg: "the interview is not awaiting review"}
		}
		var status, answerID string
		var evalID *string
		if err := tx.QueryRow(ctx, `SELECT status, answer_id, evaluation_id FROM ati.review_tasks WHERE id = $1 FOR UPDATE`, taskID).Scan(&status, &answerID, &evalID); err != nil {
			return err
		}
		if status != "open" {
			return &ConflictError{Code: "review_already_completed", Msg: "this answer has already been reviewed"}
		}
		a, err := s.loadAnswer(ctx, tx, sess, answerID)
		if err != nil {
			return err
		}
		var rubric *evaluation.Rubric
		var centi *int
		var scores []byte
		if a.RubricID == nil || a.RubricVersion == nil {
			if dec.Scores != nil {
				return &ValidationError{"the answer's question has no rubric: record a rationale without scores"}
			}
		} else {
			if rubric, _, err = s.loadRubric(ctx, tx, *a.RubricID, *a.RubricVersion); err != nil {
				return err
			}
			c, err := evaluation.WeightedCenti(dec.Scores, rubric)
			if err != nil {
				return &ValidationError{err.Error()}
			}
			centi = &c
			scores, _ = json.Marshal(dec.Scores)
		}
		var priorScores json.RawMessage
		var priorCenti *int
		if evalID != nil {
			var c int
			if err := tx.QueryRow(ctx, `SELECT scores, weighted_score_centi FROM ati.evaluations WHERE id = $1`, *evalID).Scan(&priorScores, &c); err != nil {
				return err
			}
			priorCenti = &c
		}
		now := s.now().UTC()
		out.OutcomeID = ids.New(ids.ReviewOutcome)
		var scoresArg any
		if scores != nil {
			scoresArg = scores
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.review_outcomes (id, task_id, tenant_id, session_id, answer_id, reviewer_id, rubric_id, rubric_version,
			scores, weighted_score_centi, prior_evaluation_id, prior_scores, prior_weighted_score_centi, rationale, decided_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, out.OutcomeID, taskID, sess.TenantID, sess.ID, answerID, reviewerID,
			a.RubricID, a.RubricVersion, scoresArg, centi, evalID, priorScores, priorCenti, dec.Rationale, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ati.review_tasks SET status = 'completed', completed_at = $2 WHERE id = $1`, taskID, now); err != nil {
			return err
		}
		details := map[string]any{"task_id": taskID, "answer_id": answerID, "rubric": nil, "weighted_score_centi": centi}
		if rubric != nil {
			details["rubric"] = rubric.Ref()
		}
		if priorCenti != nil {
			details["prior_weighted_score_centi"], details["changed_from_ai"] = *priorCenti, string(priorScores) != string(scores)
		}
		if err := s.audit(ctx, tx, sess.TenantID, actor, "review.outcome", "interview_session", sess.ID, "allowed", details); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ati.review_tasks WHERE session_id = $1 AND status = 'open'`, sess.ID).Scan(&out.OpenTasks); err != nil {
			return err
		}
		out.InterviewState = sess.State
		if out.OpenTasks > 0 {
			return nil
		}
		if err := s.writeReport(ctx, tx, sess, now); err != nil {
			return err
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.cause = "review_completed"
		if err := s.fire(ctx, oc, flow.EvReviewCompleted); err != nil {
			return err
		}
		out.InterviewState = oc.sess.State
		return s.audit(ctx, tx, sess.TenantID, actor, "report.created", "interview_session", sess.ID, "allowed", nil)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReportNotice states what a report is (ADR-022, ADR-008).
const ReportNotice = "Decision support only: reviewer-confirmed rubric scores with the evidence behind them. " +
	"It contains no hiring recommendation and must not be the sole basis of a decision."

func (s *Service) writeReport(ctx context.Context, tx pgx.Tx, sess *sessionRow, now time.Time) error {
	plan, err := s.plans.load(ctx, tx, sess.PlanID, sess.PlanVersion)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT t.answer_id, a.item_id, q.text, o.rubric_id || '@' || o.rubric_version, r.synthetic, o.scores, o.weighted_score_centi,
			o.rationale, o.reviewer_id, o.decided_at, t.reasons, e.id, e.evaluator_version, e.scores, e.weighted_score_centi, e.confidence, e.routing_reasons, e.evidence
		FROM ati.review_tasks t
		JOIN ati.review_outcomes o ON o.task_id = t.id
		JOIN ati.candidate_answers a ON a.id = t.answer_id
		JOIN ati.interview_questions q ON q.plan_id = $2 AND q.plan_version = $3 AND q.item_id = a.item_id
		LEFT JOIN ati.rubrics r ON r.rubric_id = o.rubric_id AND r.version = o.rubric_version
		LEFT JOIN ati.evaluations e ON e.id = t.evaluation_id
		WHERE t.session_id = $1 ORDER BY a.finalized_at, a.id`, sess.ID, sess.PlanID, sess.PlanVersion)
	if err != nil {
		return err
	}
	type aiPart struct {
		EvaluationID     string          `json:"evaluation_id"`
		EvaluatorVersion string          `json:"evaluator_version"`
		Scores           map[string]int  `json:"scores"`
		WeightedScore    float64         `json:"weighted_score"`
		Confidence       float64         `json:"confidence"`
		Calibrated       bool            `json:"calibrated"`
		RoutingReasons   []string        `json:"routing_reasons"`
		Evidence         json.RawMessage `json:"evidence"`
	}
	type reviewed struct {
		Scores        map[string]int `json:"scores"` // null: the question has no rubric
		WeightedScore *float64       `json:"weighted_score"`
		Rationale     string         `json:"rationale"`
		ReviewerID    string         `json:"reviewer_id"`
		DecidedAt     time.Time      `json:"decided_at"`
	}
	type answerPart struct {
		AnswerID        string   `json:"answer_id"`
		ItemID          string   `json:"item_id"`
		Question        string   `json:"question"`
		Rubric          *string  `json:"rubric"`
		RubricSynthetic *bool    `json:"rubric_synthetic"`
		Reviewed        reviewed `json:"reviewed"`
		ReviewReasons   []string `json:"review_reasons"`
		AIEvaluation    *aiPart  `json:"ai_evaluation"`
	}
	answers := []answerPart{}
	for rows.Next() {
		var p answerPart
		var centi *int
		var evalID, evaluator *string
		var aiScores, aiEvidence json.RawMessage
		var aiCenti *int
		var aiConfidence *float64
		var aiReasons []string
		if err := rows.Scan(&p.AnswerID, &p.ItemID, &p.Question, &p.Rubric, &p.RubricSynthetic, &p.Reviewed.Scores, &centi, &p.Reviewed.Rationale,
			&p.Reviewed.ReviewerID, &p.Reviewed.DecidedAt, &p.ReviewReasons, &evalID, &evaluator, &aiScores, &aiCenti, &aiConfidence, &aiReasons, &aiEvidence); err != nil {
			rows.Close()
			return err
		}
		p.Reviewed.WeightedScore = centiPoints(centi)
		if evalID != nil {
			ai := &aiPart{EvaluationID: *evalID, EvaluatorVersion: *evaluator, WeightedScore: float64(*aiCenti) / 100, Confidence: *aiConfidence,
				RoutingReasons: aiReasons, Evidence: aiEvidence}
			if err := json.Unmarshal(aiScores, &ai.Scores); err != nil {
				rows.Close()
				return err
			}
			p.AIEvaluation = ai
		}
		answers = append(answers, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"report_version": 1, "interview_id": sess.ID, "generated_at": now,
		"plan": map[string]any{"plan_id": plan.ID, "version": plan.Version, "synthetic": plan.Synthetic}, "language": sess.NoticeLanguage,
		"decision_support_only": true, "notice": ReportNotice, "answers": answers,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ati.reports (id, tenant_id, session_id, body, created_at) VALUES ($1,$2,$3,$4,$5)`, ids.New(ids.Report), sess.TenantID, sess.ID, body, now)
	return err
}

// centiPoints converts hundredths of a point to points; nil stays nil.
func centiPoints(centi *int) *float64 {
	if centi == nil {
		return nil
	}
	v := float64(*centi) / 100
	return &v
}

// Report returns the report of one of the tenant's interviews, once it exists (REPORT_READY).
func (s *Service) Report(ctx context.Context, tenantID, sessionID string) (json.RawMessage, error) {
	var body json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT body FROM ati.reports WHERE session_id = $1 AND tenant_id = $2`, sessionID, tenantID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return body, err
}
