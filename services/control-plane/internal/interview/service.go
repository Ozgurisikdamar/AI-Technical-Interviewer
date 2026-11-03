// Package interview is the control-plane application service: it executes the flow engine's
// transitions inside database transactions, applies their effects, and records events, audit
// entries and outbox handoff events atomically. External effects (LiveKit room deletion, stopping
// recordings) run only after commit.
package interview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

// Settings are the policy values the service enforces.
type Settings struct {
	CompletionDeadline  time.Duration
	PauseTimeout        time.Duration
	InvitationTTL       time.Duration
	CandidateSessionTTL time.Duration
	LeaseTTL            time.Duration
	Retention           config.Retention
	RecordingEnabled    bool
	RecordingAttested   bool
	NoticeID            string
	NoticeVersion       string
	// FutureToleranceMs bounds clock skew for agent-reported timestamps.
	FutureToleranceMs int64
	// PromptDeviationThreshold is an uncalibrated placeholder (ADR-014).
	PromptDeviationThreshold float64
}

// Effects are external side effects executed after a transaction commits.
type Effects interface {
	DeleteRoom(ctx context.Context, sessionID, room string)
	StopRecording(ctx context.Context, sessionID string)
	StartRecording(ctx context.Context, sessionID string)
}

// NopEffects ignores external effects (tests, tools).
type NopEffects struct{}

func (NopEffects) DeleteRoom(context.Context, string, string) {}
func (NopEffects) StopRecording(context.Context, string)      {}
func (NopEffects) StartRecording(context.Context, string)     {}

// Actor identifies who is acting.
type Actor struct {
	Type      flow.Actor
	ID        string
	RequestID string
}

// LeaseRef is the Tier-1 lease presented with an internal request (verified inside the transaction).
type LeaseRef struct {
	LeaseID      string
	FencingToken int64
}

// Service is the interview application service.
type Service struct {
	pool     *pgxpool.Pool
	machine  *flow.Machine
	settings Settings
	effects  Effects
	log      *slog.Logger
	now      func() time.Time
	plans    *planCache
	notices  *Notices
	observer Observer

	recordingActive func(ctx context.Context, sessionID string) bool
}

// New builds the service and verifies that every guard and effect of the contract is implemented.
func New(pool *pgxpool.Pool, machine *flow.Machine, settings Settings, effects Effects, log *slog.Logger, now func() time.Time) (*Service, error) {
	if now == nil {
		now = time.Now
	}
	if effects == nil {
		effects = NopEffects{}
	}
	if settings.FutureToleranceMs == 0 {
		settings.FutureToleranceMs = 2000
	}
	if settings.PromptDeviationThreshold == 0 {
		settings.PromptDeviationThreshold = 0.6
	}
	implemented := map[string]bool{}
	for _, g := range implementedGuards {
		implemented[g] = true
	}
	if err := machine.RequireGuards(implemented); err != nil {
		return nil, err
	}
	for _, t := range machine.Transitions() {
		for _, e := range t.Effects {
			if !slices.Contains(implementedEffects, e) {
				return nil, fmt.Errorf("effect %q of %s is not implemented", e, t.ID)
			}
		}
	}
	notices, err := LoadNotices()
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, machine: machine, settings: settings, effects: effects, log: log, now: now, plans: newPlanCache(), notices: notices}, nil
}

// Machine exposes the loaded flow engine.
func (s *Service) Machine() *flow.Machine { return s.machine }

// Errors returned by the service. The API layer maps them to problem codes.
var (
	ErrNotFound           = errors.New("not found")
	ErrStaleStateVersion  = errors.New("stale state version")
	ErrInvitationInvalid  = errors.New("invitation invalid or expired")
	ErrRetentionMissing   = errors.New("explicit retention policy is not configured")
	ErrPlanUnavailable    = errors.New("plan unavailable")
	ErrLeaseStale         = errors.New("lease is not current")
	ErrSegmentConflict    = errors.New("segment key reused with different content")
	ErrManifestIncomplete = errors.New("flush manifest lists segments that were not received")
	ErrNonceInvalid       = errors.New("claim nonce invalid, expired or already used")
)

// TransitionRefusedError wraps a flow-engine refusal.
type TransitionRefusedError struct {
	State flow.State
	Event flow.Event
	Err   error
}

func (e *TransitionRefusedError) Error() string {
	return fmt.Sprintf("transition %s not allowed from %s: %v", e.Event, e.State, e.Err)
}
func (e *TransitionRefusedError) Unwrap() error { return e.Err }

// ConflictError is a well-defined business conflict with a stable code.
type ConflictError struct {
	Code string
	Msg  string
}

func (e *ConflictError) Error() string { return e.Code + ": " + e.Msg }

// ValidationError is a malformed request.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// SegmentRejectedError means a segment was refused and recorded as an integrity event.
type SegmentRejectedError struct{ Reason string }

func (e *SegmentRejectedError) Error() string { return "segment rejected: " + e.Reason }

// sessionRow mirrors ati.interview_sessions.
type sessionRow struct {
	ID              string
	TenantID        string
	CandidateID     string
	PlanID          string
	PlanVersion     int
	NoticeID        string
	NoticeVersion   string
	NoticeLanguage  string
	State           flow.State
	StateVersion    int64
	InputMode       *string
	PausedFrom      *string
	PausedAt        *time.Time
	TimelineEpoch   *time.Time
	RoomName        string
	SegmentSeq      int64
	LeaseFencing    int64
	Retention       json.RawMessage
	RecordingPolicy json.RawMessage
	CompletedAt     *time.Time
	CancelledAt     *time.Time
	CancelReason    *string
}

const sessionColumns = `id, tenant_id, candidate_id, plan_id, plan_version, notice_id, notice_version, notice_language, state, state_version,
	input_mode, paused_from, paused_at, timeline_epoch, room_name, segment_seq, lease_fencing, retention, recording_policy,
	completed_at, cancelled_at, cancel_reason`

func scanSession(row pgx.Row) (*sessionRow, error) {
	var s sessionRow
	var state string
	err := row.Scan(&s.ID, &s.TenantID, &s.CandidateID, &s.PlanID, &s.PlanVersion, &s.NoticeID, &s.NoticeVersion, &s.NoticeLanguage,
		&state, &s.StateVersion, &s.InputMode, &s.PausedFrom, &s.PausedAt, &s.TimelineEpoch, &s.RoomName, &s.SegmentSeq,
		&s.LeaseFencing, &s.Retention, &s.RecordingPolicy, &s.CompletedAt, &s.CancelledAt, &s.CancelReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.State = flow.State(state)
	return &s, nil
}

func (s *Service) lockSession(ctx context.Context, tx pgx.Tx, id string) (*sessionRow, error) {
	return scanSession(tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM ati.interview_sessions WHERE id = $1 FOR UPDATE`, id))
}

func (s *Service) getSession(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (*sessionRow, error) {
	return scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM ati.interview_sessions WHERE id = $1`, id))
}

// nowMs is the current position on the session timeline.
func (s *Service) nowMs(sess *sessionRow, now time.Time) int64 {
	if sess.TimelineEpoch == nil {
		return 0
	}
	ms := now.Sub(*sess.TimelineEpoch).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

// inTx runs fn in a transaction and executes queued external effects after commit.
func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx, after *[]func(context.Context)) error) error {
	var after []func(context.Context)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx, &after); err != nil {
		var keep *committedError
		if errors.As(err, &keep) {
			if cerr := tx.Commit(ctx); cerr != nil {
				return cerr
			}
			return keep.err
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, f := range after {
		f(context.WithoutCancel(ctx))
	}
	return nil
}

// committedError makes inTx commit the transaction (e.g., to keep an integrity event) and still
// return the wrapped error to the caller.
type committedError struct{ err error }

func (e *committedError) Error() string { return e.err.Error() }
func (e *committedError) Unwrap() error { return e.err }

func commitThen(err error) error { return &committedError{err: err} }

func (s *Service) audit(ctx context.Context, tx pgx.Tx, tenantID string, actor Actor, action, subjectType, subjectID, outcome string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO ati.audit_logs (tenant_id, actor_type, actor_id, action, subject_type, subject_id, outcome, request_id, details)
		VALUES (NULLIF($1,''),$2,NULLIF($3,''),$4,$5,NULLIF($6,''),$7,NULLIF($8,''),$9)`,
		tenantID, string(actor.Type), actor.ID, action, subjectType, subjectID, outcome, actor.RequestID, b)
	return err
}

// AuditDenied records a refused operation in its own transaction (the refused one rolled back).
func (s *Service) AuditDenied(ctx context.Context, sessionID string, actor Actor, action string, reason error) {
	_ = s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		var tenant string
		_ = tx.QueryRow(ctx, `SELECT tenant_id FROM ati.interview_sessions WHERE id = $1`, sessionID).Scan(&tenant)
		return s.audit(ctx, tx, tenant, actor, action, "interview_session", sessionID, "denied", map[string]any{"reason": reason.Error()})
	})
}

func (s *Service) integrity(ctx context.Context, tx pgx.Tx, sessionID, kind string, details map[string]any) error {
	b, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO ati.integrity_events (session_id, kind, details) VALUES ($1,$2,$3)`, sessionID, kind, b)
	return err
}

func checkVersion(sess *sessionRow, expected int64) error {
	if expected != sess.StateVersion {
		return fmt.Errorf("%w: expected %d, current %d", ErrStaleStateVersion, expected, sess.StateVersion)
	}
	return nil
}

// checkLease verifies, inside the transaction, that the lease is the session's current one.
func (s *Service) checkLease(ctx context.Context, tx pgx.Tx, sess *sessionRow, lease LeaseRef, now time.Time) error {
	var fencing int64
	var expires time.Time
	var ended *time.Time
	err := tx.QueryRow(ctx, `SELECT fencing_token, expires_at, ended_at FROM ati.agent_leases WHERE id = $1 AND session_id = $2`, lease.LeaseID, sess.ID).Scan(&fencing, &expires, &ended)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseStale
	}
	if err != nil {
		return err
	}
	if ended != nil || !expires.After(now) || fencing != lease.FencingToken || fencing != sess.LeaseFencing {
		return ErrLeaseStale
	}
	return nil
}
