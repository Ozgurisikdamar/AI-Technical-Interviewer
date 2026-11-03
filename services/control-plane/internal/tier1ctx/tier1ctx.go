// Package tier1ctx builds the Tier-1 runtime context (contract tier1-context/v1).
//
// It reads only the allowlisted views through the restricted ati_tier1_ctx database role, and
// validates every context against the contract before returning it (fail closed). It must not
// depend on recording, administration or evaluation code (checked by internal/archtest).
package tier1ctx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
)

// ErrNotAvailable means the session has no Tier-1 context (not started, or unknown).
var ErrNotAvailable = errors.New("no tier-1 context for this session")

// Prompt is the active approved prompt.
type Prompt struct {
	ActivationID  string `json:"activation_id"`
	ItemRef       string `json:"item_ref"`
	Kind          string `json:"kind"`
	Text          string `json:"text"`
	TurnProfile   string `json:"turn_profile"`
	PromptStarted bool   `json:"prompt_started"`
}

// CompletedPrompt is a question already answered (question text only).
type CompletedPrompt struct {
	ItemRef string `json:"item_ref"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

// PendingCompletion is a candidate "Finish answer" request awaiting the agent's flush.
type PendingCompletion struct {
	ActivationID   string `json:"activation_id"`
	RequestedBy    string `json:"requested_by"`
	DeadlineUnixMs int64  `json:"deadline_unix_ms"`
}

// TextInput is untrusted candidate text (text mode). It must reach the model as user input only.
type TextInput struct {
	SegmentID     string `json:"segment_id"`
	ActivationID  string `json:"activation_id"`
	UntrustedText string `json:"untrusted_text"`
}

// Policy is the fixed candidate-facing policy.
type Policy struct {
	HintsAllowed   bool   `json:"hints_allowed"`
	FollowUpSource string `json:"follow_up_source"`
}

// Timeline anchors the session clock.
type Timeline struct {
	EpochUnixMs     *int64 `json:"epoch_unix_ms"`
	ServerNowUnixMs int64  `json:"server_now_unix_ms"`
}

// Context is the complete allowlist sent to the Tier-1 agent.
type Context struct {
	ContextSchema            string             `json:"context_schema"`
	SessionRef               string             `json:"session_ref"`
	State                    string             `json:"state"`
	StateVersion             int64              `json:"state_version"`
	Language                 string             `json:"language"`
	SeniorityBand            string             `json:"seniority_band"`
	Modality                 string             `json:"modality"`
	InputMode                string             `json:"input_mode"`
	RecordingActive          bool               `json:"recording_active"`
	Policy                   Policy             `json:"policy"`
	ActivePrompt             *Prompt            `json:"active_prompt"`
	CompletedPrompts         []CompletedPrompt  `json:"completed_prompts"`
	PendingCompletionRequest *PendingCompletion `json:"pending_completion_request"`
	PendingTextInputs        []TextInput        `json:"pending_text_inputs"`
	Timeline                 Timeline           `json:"timeline"`
}

// Builder reads the Tier-1 views.
type Builder struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// Hooks for later phases (recording, text inputs) supply data through their own views.
	recordingActive func(ctx context.Context, q pgx.Tx, sessionID string) (bool, error)
	textInputs      func(ctx context.Context, q pgx.Tx, sessionID string) ([]TextInput, error)
}

// New returns a builder over a pool connected as ati_tier1_ctx.
func New(pool *pgxpool.Pool, now func() time.Time) *Builder {
	if now == nil {
		now = time.Now
	}
	return &Builder{pool: pool, now: now, recordingActive: recordingFromView, textInputs: textInputsFromView}
}

// Build returns the validated context for a session.
func (b *Builder) Build(ctx context.Context, sessionID string) (*Context, error) {
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	c := &Context{
		ContextSchema:     "tier1-context/v1",
		SessionRef:        sessionID,
		Modality:          "voice_qa",
		Policy:            Policy{HintsAllowed: false, FollowUpSource: "approved_plan_only"},
		CompletedPrompts:  []CompletedPrompt{},
		PendingTextInputs: []TextInput{},
	}
	var inputMode *string
	err = tx.QueryRow(ctx, `SELECT state, state_version, language, seniority_band, input_mode, epoch_unix_ms FROM ati.tier1_session_v WHERE session_id = $1`, sessionID).
		Scan(&c.State, &c.StateVersion, &c.Language, &c.SeniorityBand, &inputMode, &c.Timeline.EpochUnixMs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotAvailable
	}
	if err != nil {
		return nil, err
	}
	if inputMode != nil {
		c.InputMode = *inputMode
	} else {
		c.InputMode = "voice" // CANCELLED before start; the context is informational only
	}
	var p Prompt
	err = tx.QueryRow(ctx, `SELECT activation_id, item_id, kind, text, turn_profile, prompt_started FROM ati.tier1_active_prompt_v WHERE session_id = $1`, sessionID).
		Scan(&p.ActivationID, &p.ItemRef, &p.Kind, &p.Text, &p.TurnProfile, &p.PromptStarted)
	switch {
	case err == nil:
		c.ActivePrompt = &p
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT item_id, kind, text FROM ati.tier1_completed_prompts_v WHERE session_id = $1 ORDER BY ordinal`, sessionID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cp CompletedPrompt
		if err := rows.Scan(&cp.ItemRef, &cp.Kind, &cp.Text); err != nil {
			rows.Close()
			return nil, err
		}
		c.CompletedPrompts = append(c.CompletedPrompts, cp)
	}
	rows.Close()
	var pc PendingCompletion
	err = tx.QueryRow(ctx, `SELECT activation_id, deadline_unix_ms FROM ati.tier1_pending_completion_v WHERE session_id = $1`, sessionID).Scan(&pc.ActivationID, &pc.DeadlineUnixMs)
	switch {
	case err == nil:
		pc.RequestedBy = "candidate"
		c.PendingCompletionRequest = &pc
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	if b.recordingActive != nil {
		if c.RecordingActive, err = b.recordingActive(ctx, tx, sessionID); err != nil {
			return nil, err
		}
	}
	if b.textInputs != nil {
		if c.PendingTextInputs, err = b.textInputs(ctx, tx, sessionID); err != nil {
			return nil, err
		}
	}
	c.Timeline.ServerNowUnixMs = b.now().UnixMilli()
	if err := schema.ValidateValue(contracts.Tier1ContextSchemaPath, c); err != nil {
		return nil, fmt.Errorf("tier-1 context violates its contract (refusing to send): %w", err)
	}
	return c, nil
}

// recordingFromView reads tier1_recording_v: whether a consented recording is running (ADR-017).
func recordingFromView(ctx context.Context, q pgx.Tx, sessionID string) (bool, error) {
	var active bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.tier1_recording_v WHERE session_id = $1)`, sessionID).Scan(&active)
	return active, err
}

// textInputsFromView reads tier1_text_inputs_v: the candidate's text answers to the open question
// (text mode, ADR-018), oldest first, at most 20. They are untrusted user input for the model.
func textInputsFromView(ctx context.Context, q pgx.Tx, sessionID string) ([]TextInput, error) {
	rows, err := q.Query(ctx, `SELECT segment_id, activation_id, untrusted_text FROM (
		SELECT segment_id, activation_id, untrusted_text, sequence FROM ati.tier1_text_inputs_v WHERE session_id = $1 ORDER BY sequence DESC LIMIT 20
	) newest ORDER BY sequence`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TextInput{}
	for rows.Next() {
		var t TextInput
		if err := rows.Scan(&t.SegmentID, &t.ActivationID, &t.UntrustedText); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
