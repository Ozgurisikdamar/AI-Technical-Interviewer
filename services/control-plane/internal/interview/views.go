package interview

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

// Progress counts finalized answers against plan items.
type Progress struct {
	CompletedItems int `json:"completed_items"`
	TotalItems     int `json:"total_items"`
}

// ActivePromptView is the open prompt shown to the candidate for accessibility.
type ActivePromptView struct {
	ActivationID string `json:"activation_id"`
	Kind         string `json:"kind"`
	Text         string `json:"text"`
}

// CandidateView is the candidate's projection (OpenAPI InterviewCandidateView). It never contains
// scores, rubric references, evaluation data or other candidates' data.
type CandidateView struct {
	ID                string            `json:"id"`
	State             flow.State        `json:"state"`
	StateVersion      int64             `json:"state_version"`
	Progress          Progress          `json:"progress"`
	InputMode         *string           `json:"input_mode"`
	ActivePrompt      *ActivePromptView `json:"active_prompt"`
	PausedFrom        *string           `json:"paused_from"`
	PendingCompletion bool              `json:"pending_completion"`
	RecordingActive   bool              `json:"recording_active"`
	AllowedActions    []string          `json:"allowed_actions"`
	Environment       string            `json:"environment"`
	// The candidate's application, as the sign-in matched it (ADR-024): the number as shown
	// (XXXX-XXXX, null for interviews created before it), the interview's title, and the language
	// the interview is held in, which can differ from the page's.
	ApplicationNumber *string `json:"application_number"`
	InterviewTitle    string  `json:"interview_title"`
	Language          string  `json:"language"`
}

// StaffView is the staff projection (OpenAPI InterviewStaffView).
type StaffView struct {
	ID           string     `json:"id"`
	State        flow.State `json:"state"`
	StateVersion int64      `json:"state_version"`
	CandidateRef string     `json:"candidate_ref"`
	// ApplicationNumber is the number the candidate signs in with, as shown (ADR-024).
	ApplicationNumber *string `json:"application_number"`
	Plan              struct {
		PlanID    string `json:"plan_id"`
		Version   int    `json:"version"`
		Synthetic bool   `json:"synthetic"`
	} `json:"plan"`
	InputMode *string `json:"input_mode"`
	Recording struct {
		Consented *bool `json:"consented"`
		Active    bool  `json:"active"`
	} `json:"recording"`
	Progress  Progress  `json:"progress"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Service) progress(ctx context.Context, sess *sessionRow) (Progress, error) {
	var p Progress
	err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ati.candidate_answers WHERE session_id = $1),
		(SELECT count(*) FROM ati.interview_questions WHERE plan_id = $2 AND plan_version = $3)`, sess.ID, sess.PlanID, sess.PlanVersion).
		Scan(&p.CompletedItems, &p.TotalItems)
	return p, err
}

// RecordingActive reports whether a recording is currently running (ADR-017).
func (s *Service) RecordingActive(ctx context.Context, sessionID string) bool {
	if s.recordingActive != nil {
		return s.recordingActive(ctx, sessionID)
	}
	var live bool
	_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.audio_recordings WHERE session_id = $1 AND status IN ('starting','active'))`, sessionID).Scan(&live)
	return live
}

// SetRecordingProbe installs the recording-status lookup (internal/recording).
func (s *Service) SetRecordingProbe(f func(ctx context.Context, sessionID string) bool) {
	s.recordingActive = f
}

// CandidateView builds the candidate projection.
func (s *Service) CandidateView(ctx context.Context, sessionID, env string) (*CandidateView, error) {
	sess, err := s.getSession(ctx, s.pool, sessionID)
	if err != nil {
		return nil, err
	}
	v := &CandidateView{ID: sess.ID, State: sess.State, StateVersion: sess.StateVersion, InputMode: sess.InputMode, PausedFrom: sess.PausedFrom, Environment: env,
		ApplicationNumber: shownNumber(sess.ApplicationNumber)}
	plan, err := s.plans.load(ctx, s.pool, sess.PlanID, sess.PlanVersion)
	if err != nil {
		return nil, err
	}
	v.InterviewTitle, v.Language = plan.Title, plan.Language
	if v.Progress, err = s.progress(ctx, sess); err != nil {
		return nil, err
	}
	var ap ActivePromptView
	err = s.pool.QueryRow(ctx, `SELECT a.id, a.kind, q.text FROM ati.question_activations a
		JOIN ati.interview_questions q ON q.plan_id = $2 AND q.plan_version = $3 AND q.item_id = a.item_id
		WHERE a.session_id = $1 AND a.closed_at IS NULL`, sess.ID, sess.PlanID, sess.PlanVersion).Scan(&ap.ActivationID, &ap.Kind, &ap.Text)
	switch {
	case err == nil:
		v.ActivePrompt = &ap
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.completion_requests WHERE session_id = $1 AND resolved_at IS NULL)`, sess.ID).Scan(&v.PendingCompletion); err != nil {
		return nil, err
	}
	v.RecordingActive = s.RecordingActive(ctx, sess.ID)
	v.AllowedActions = s.candidateActions(sess, v.PendingCompletion)
	return v, nil
}

var eventActions = map[flow.Event]string{
	flow.EvConsentAccepted: "consent", flow.EvStart: "start", flow.EvCalibrationComplete: "begin",
	flow.EvPause: "pause", flow.EvResume: "resume", flow.EvWithdraw: "withdraw",
}

// candidateActions derives the candidate's available actions from the flow contract.
func (s *Service) candidateActions(sess *sessionRow, pending bool) []string {
	set := map[string]bool{}
	for _, t := range s.machine.Transitions() {
		if t.From != sess.State || !t.Allows(flow.ActorCandidate) || t.Phase > s.machine.MaxPhase {
			continue
		}
		if a, ok := eventActions[t.Event]; ok {
			set[a] = true
		}
	}
	if req, ok := s.machine.Request("R01"); ok && slices.Contains(req.AllowedIn, sess.State) && !pending {
		set["finish_answer"] = true
	}
	switch sess.State {
	case flow.Calibration, flow.QuestionActive, flow.CandidateSpeaking, flow.Paused:
		set["join_media"] = true
	}
	if sess.InputMode != nil && *sess.InputMode == "text" && (sess.State == flow.QuestionActive || sess.State == flow.CandidateSpeaking) {
		set["text_response"] = true
	}
	out := make([]string, 0, len(set))
	for _, a := range []string{"consent", "start", "begin", "pause", "resume", "finish_answer", "withdraw", "text_response", "join_media"} {
		if set[a] {
			out = append(out, a)
		}
	}
	return out
}

// shownNumber is an application number as people see it (XXXX-XXXX); nil stays nil.
func shownNumber(canonical *string) *string {
	if canonical == nil {
		return nil
	}
	shown := accesscode.FormatNumber(*canonical)
	return &shown
}

// StaffView builds the staff projection, scoped to the caller's tenant (404 otherwise).
func (s *Service) StaffView(ctx context.Context, sessionID, tenantID string) (*StaffView, error) {
	sess, err := s.getSession(ctx, s.pool, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.TenantID != tenantID {
		return nil, ErrNotFound
	}
	v := &StaffView{ID: sess.ID, State: sess.State, StateVersion: sess.StateVersion, InputMode: sess.InputMode, ApplicationNumber: shownNumber(sess.ApplicationNumber)}
	plan, err := s.plans.load(ctx, s.pool, sess.PlanID, sess.PlanVersion)
	if err != nil {
		return nil, err
	}
	v.Plan.PlanID, v.Plan.Version, v.Plan.Synthetic = plan.ID, plan.Version, plan.Synthetic
	if err := s.pool.QueryRow(ctx, `SELECT c.external_ref, s.created_at, s.updated_at FROM ati.interview_sessions s JOIN ati.candidates c ON c.id = s.candidate_id WHERE s.id = $1`, sess.ID).
		Scan(&v.CandidateRef, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	var consented *bool
	err = s.pool.QueryRow(ctx, `SELECT granted FROM ati.consent_records WHERE session_id = $1 AND purpose_id = 'audio_recording' ORDER BY decided_at DESC LIMIT 1`, sess.ID).Scan(&consented)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	v.Recording.Consented = consented
	v.Recording.Active = s.RecordingActive(ctx, sess.ID)
	if v.Progress, err = s.progress(ctx, sess); err != nil {
		return nil, err
	}
	return v, nil
}
