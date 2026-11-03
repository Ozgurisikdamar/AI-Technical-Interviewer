// Package recording implements the optional, fail-closed recording of the candidate's microphone
// (ADR-017). A session is recorded only if every precondition holds; otherwise nothing is started
// and the reasons are audited. Only the candidate's microphone track is recorded, only while a
// question is open, and recordings are purged when their retention ends (tombstones remain).
package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
)

// Settings are the deployment-level preconditions.
type Settings struct {
	Enabled            bool
	EncryptionAttested bool
	RetentionDays      int
	// EgressTimeout bounds every egress API call (default DefaultEgressTimeout). LiveKit never
	// answers a start request when no egress service is connected; the bound turns that into a
	// failed recording instead of one left "starting".
	EgressTimeout time.Duration
}

// DefaultEgressTimeout is used when Settings.EgressTimeout is zero (uncalibrated).
const DefaultEgressTimeout = 10 * time.Second

// Skip reasons (audited as recording.skipped).
const (
	ReasonFeatureDisabled       = "feature_disabled"
	ReasonTenantNotAllowed      = "tenant_not_allowed"
	ReasonNoConsent             = "no_consent"
	ReasonNoRetention           = "retention_not_configured"
	ReasonEncryptionNotAttested = "encryption_not_attested"
	ReasonNotVoice              = "not_voice_mode"
	ReasonNotAnswering          = "no_open_question"
	ReasonNoMicrophone          = "no_microphone_track"
)

// Recorder starts and stops recordings. It is safe for concurrent use.
type Recorder struct {
	pool   *pgxpool.Pool
	media  realtime.Media
	egress realtime.Egress
	set    Settings
	log    *slog.Logger
	now    func() time.Time
}

// New returns a recorder. A nil egress makes every start fail closed.
func New(pool *pgxpool.Pool, media realtime.Media, egress realtime.Egress, set Settings, log *slog.Logger, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	if set.EgressTimeout <= 0 {
		set.EgressTimeout = DefaultEgressTimeout
	}
	return &Recorder{pool: pool, media: media, egress: egress, set: set, log: log, now: now}
}

// call runs one egress API call within the egress timeout.
func (r *Recorder) call(ctx context.Context, fn func(ctx context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, r.set.EgressTimeout)
	defer cancel()
	return fn(c)
}

type session struct {
	ID, TenantID, State, Room string
	InputMode                 *string
	Epoch                     *time.Time
	Policy                    json.RawMessage
	TenantAllows              bool
	Consented                 bool
}

func (r *Recorder) load(ctx context.Context, tx pgx.Tx, sessionID string) (*session, error) {
	var s session
	err := tx.QueryRow(ctx, `SELECT s.id, s.tenant_id, s.state, s.room_name, s.input_mode, s.timeline_epoch, s.recording_policy, t.recording_allowed,
			EXISTS (SELECT 1 FROM ati.consent_records c WHERE c.session_id = s.id AND c.notice_version = s.notice_version
			        AND c.purpose_id = 'audio_recording' AND c.granted)
		FROM ati.interview_sessions s JOIN ati.tenants t ON t.id = s.tenant_id
		WHERE s.id = $1 FOR UPDATE OF s`, sessionID).
		Scan(&s.ID, &s.TenantID, &s.State, &s.Room, &s.InputMode, &s.Epoch, &s.Policy, &s.TenantAllows, &s.Consented)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Decide evaluates every precondition (all reasons are reported, not just the first).
func (r *Recorder) Decide(s *session) []string {
	var policy struct {
		Enabled            bool `json:"enabled"`
		EncryptionAttested bool `json:"encryption_attested"`
	}
	_ = json.Unmarshal(s.Policy, &policy)
	var reasons []string
	if !r.set.Enabled || !policy.Enabled { // the session's policy snapshot and the current deployment must both allow it
		reasons = append(reasons, ReasonFeatureDisabled)
	}
	if !s.TenantAllows {
		reasons = append(reasons, ReasonTenantNotAllowed)
	}
	if !s.Consented {
		reasons = append(reasons, ReasonNoConsent)
	}
	if r.set.RetentionDays <= 0 {
		reasons = append(reasons, ReasonNoRetention)
	}
	if !r.set.EncryptionAttested || !policy.EncryptionAttested {
		reasons = append(reasons, ReasonEncryptionNotAttested)
	}
	if s.InputMode == nil || *s.InputMode != "voice" {
		reasons = append(reasons, ReasonNotVoice)
	}
	if s.State != "QUESTION_ACTIVE" && s.State != "CANDIDATE_SPEAKING" {
		reasons = append(reasons, ReasonNotAnswering)
	}
	return reasons
}

func (r *Recorder) timelineMs(s *session, at time.Time) *int64 {
	if s.Epoch == nil {
		return nil
	}
	ms := at.Sub(*s.Epoch).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return &ms
}

func audit(ctx context.Context, tx pgx.Tx, tenant, sessionID, action, outcome string, details map[string]any) error {
	b, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO ati.audit_logs (tenant_id, actor_type, actor_id, action, subject_type, subject_id, outcome, details)
		VALUES ($1, 'system', 'recording', $2, 'interview_session', $3, $4, $5)`, tenant, action, sessionID, outcome, b)
	return err
}

// Start records the candidate's microphone if every precondition holds. Idempotent: a session has
// at most one live recording. Called after an activation opens (T05, T13) and when the candidate's
// microphone is published while a question is open.
// The caller's cancellation is ignored: a request that ends early must not leave a recording
// half-started; every egress call is bounded instead.
func (r *Recorder) Start(ctx context.Context, sessionID string) {
	if err := r.start(context.WithoutCancel(ctx), sessionID); err != nil {
		r.log.Warn("recording start failed", slog.String("session_id", sessionID), slog.Any("error", err))
	}
}

func (r *Recorder) start(ctx context.Context, sessionID string) error {
	var rec struct{ id, key, room, track string }
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		s, err := r.load(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		var live bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.audio_recordings WHERE session_id = $1 AND status IN ('starting','active','stopping'))`, sessionID).Scan(&live); err != nil {
			return err
		}
		if live {
			return nil
		}
		reasons := r.Decide(s)
		if len(reasons) == 0 {
			parts, err := r.media.ListParticipants(ctx, s.Room)
			if err != nil {
				return err
			}
			for _, p := range parts {
				if p.Identity == realtime.CandidateIdentity && p.MicTrackSID != "" {
					rec.track = p.MicTrackSID
				}
			}
			if rec.track == "" {
				reasons = append(reasons, ReasonNoMicrophone)
			}
		}
		if len(reasons) > 0 {
			return audit(ctx, tx, s.TenantID, sessionID, "recording.skipped", "denied", map[string]any{"reasons": reasons})
		}
		rec.id = ids.New(ids.Recording)
		rec.key = fmt.Sprintf("%s/%s/%s.ogg", s.TenantID, s.ID, rec.id)
		rec.room = s.Room
		now := r.now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO ati.audio_recordings (id, tenant_id, session_id, track_sid, object_key, status, retention_until, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,'starting',$6,$7,$7)`, rec.id, s.TenantID, s.ID, rec.track, rec.key, now.AddDate(0, 0, r.set.RetentionDays), now); err != nil {
			return err
		}
		return audit(ctx, tx, s.TenantID, sessionID, "recording.start", "allowed", map[string]any{"recording_id": rec.id})
	})
	if err != nil || rec.id == "" {
		return err
	}
	if r.egress == nil {
		return r.fail(ctx, rec.id, "no egress service configured")
	}
	var egressID string
	err = r.call(ctx, func(c context.Context) (err error) {
		egressID, err = r.egress.StartTrackEgress(c, rec.room, rec.track, rec.key)
		return err
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return r.fail(ctx, rec.id, "egress start timed out (no egress service answered)")
	}
	if err != nil {
		return r.fail(ctx, rec.id, "egress start failed")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE ati.audio_recordings SET egress_id = $2, updated_at = $3 WHERE id = $1 AND status = 'starting'`, rec.id, egressID, r.now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Stopped while the egress was starting: stop it now so nothing is captured past the stop.
		return r.call(ctx, func(c context.Context) error { return r.egress.StopEgress(c, egressID) })
	}
	return nil
}

func (r *Recorder) fail(ctx context.Context, id, why string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var tenant, sessionID string
		if err := tx.QueryRow(ctx, `UPDATE ati.audio_recordings SET status = 'failed', failure = $2, updated_at = $3 WHERE id = $1 RETURNING tenant_id, session_id`,
			id, why, r.now().UTC()).Scan(&tenant, &sessionID); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, sessionID, "recording.failed", "error", map[string]any{"recording_id": id, "reason": why})
	})
}

// Stop ends the session's live recording, if any (pause, withdrawal, completion, expiry, track loss).
func (r *Recorder) Stop(ctx context.Context, sessionID string) {
	if err := r.stop(context.WithoutCancel(ctx), sessionID); err != nil {
		r.log.Warn("recording stop failed", slog.String("session_id", sessionID), slog.Any("error", err))
	}
}

func (r *Recorder) stop(ctx context.Context, sessionID string) error {
	var id string
	var egressID *string
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		s, err := r.load(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT id, egress_id FROM ati.audio_recordings WHERE session_id = $1 AND status IN ('starting','active') FOR UPDATE`, sessionID).Scan(&id, &egressID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		status := "stopping"
		if egressID == nil {
			status = "complete" // the start is still in flight; it stops the egress when it sees this
		}
		if _, err := tx.Exec(ctx, `UPDATE ati.audio_recordings SET status = $2, ended_ms = greatest(coalesce(started_ms, 0), $3), updated_at = $4 WHERE id = $1`,
			id, status, r.timelineMs(s, r.now()), r.now().UTC()); err != nil {
			return err
		}
		return audit(ctx, tx, s.TenantID, sessionID, "recording.stop", "allowed", map[string]any{"recording_id": id})
	})
	if err != nil || id == "" || egressID == nil {
		return err
	}
	if r.egress != nil {
		if err := r.call(ctx, func(c context.Context) error { return r.egress.StopEgress(c, *egressID) }); err != nil {
			return err
		}
	}
	_, err = r.pool.Exec(ctx, `UPDATE ati.audio_recordings SET status = 'complete', updated_at = $2 WHERE id = $1 AND status = 'stopping'`, id, r.now().UTC())
	return err
}

// OnEgressEvent applies an egress lifecycle webhook (egress_started, egress_updated, egress_ended).
func (r *Recorder) OnEgressEvent(ctx context.Context, ev *realtime.Webhook) error {
	if ev.EgressID == "" {
		return nil
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var id, tenant, sessionID, status string
		var epoch *time.Time
		err := tx.QueryRow(ctx, `SELECT r.id, r.tenant_id, r.session_id, r.status, s.timeline_epoch FROM ati.audio_recordings r
			JOIN ati.interview_sessions s ON s.id = r.session_id WHERE r.egress_id = $1 FOR UPDATE OF r`, ev.EgressID).Scan(&id, &tenant, &sessionID, &status, &epoch)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not ours
		}
		if err != nil {
			return err
		}
		sess := &session{Epoch: epoch}
		at := func(ns int64) *int64 {
			if ns <= 0 {
				return r.timelineMs(sess, r.now())
			}
			return r.timelineMs(sess, time.Unix(0, ns))
		}
		now := r.now().UTC()
		switch ev.EgressStatus {
		case "EGRESS_ACTIVE":
			_, err = tx.Exec(ctx, `UPDATE ati.audio_recordings SET status = CASE WHEN status = 'starting' THEN 'active' ELSE status END,
				started_ms = coalesce(started_ms, $2), updated_at = $3 WHERE id = $1`, id, at(ev.EgressStartedAt), now)
			return err
		case "EGRESS_COMPLETE":
			_, err = tx.Exec(ctx, `UPDATE ati.audio_recordings SET status = 'complete', started_ms = coalesce(started_ms, $2),
				ended_ms = greatest(coalesce(started_ms, $2, 0), coalesce(ended_ms, $3)), updated_at = $4 WHERE id = $1`, id, at(ev.EgressStartedAt), at(ev.EgressEndedAt), now)
			return err
		case "EGRESS_FAILED", "EGRESS_ABORTED", "EGRESS_LIMIT_REACHED":
			if _, err := tx.Exec(ctx, `UPDATE ati.audio_recordings SET status = 'failed', failure = $2, updated_at = $3 WHERE id = $1`, id, ev.EgressStatus, now); err != nil {
				return err
			}
			return audit(ctx, tx, tenant, sessionID, "recording.failed", "error", map[string]any{"recording_id": id, "reason": ev.EgressStatus})
		}
		return nil
	})
}

// Active reports whether the session has a recording that may be capturing audio.
func (r *Recorder) Active(ctx context.Context, sessionID string) bool {
	var live bool
	_ = r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.audio_recordings WHERE session_id = $1 AND status IN ('starting','active'))`, sessionID).Scan(&live)
	return live
}

// ObjectStore deletes recording objects (S3-compatible storage).
type ObjectStore interface {
	Remove(ctx context.Context, key string) error
}

// PurgeExpired deletes every finished recording whose retention has ended and leaves an audited
// tombstone (the row with purged_at). Live recordings are never purged.
func PurgeExpired(ctx context.Context, pool *pgxpool.Pool, store ObjectStore, now time.Time) (int, error) {
	rows, err := pool.Query(ctx, `SELECT id, object_key FROM ati.audio_recordings
		WHERE purged_at IS NULL AND retention_until <= $1 AND status IN ('complete','failed') ORDER BY retention_until LIMIT 1000`, now.UTC())
	if err != nil {
		return 0, err
	}
	type item struct{ id, key string }
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.key); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	purged := 0
	for _, it := range items {
		if err := store.Remove(ctx, it.key); err != nil {
			return purged, fmt.Errorf("remove recording %s: %w", it.id, err)
		}
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var tenant, sessionID string
			if err := tx.QueryRow(ctx, `UPDATE ati.audio_recordings SET purged_at = $2, purge_reason = 'retention_expired', updated_at = $2
				WHERE id = $1 AND purged_at IS NULL RETURNING tenant_id, session_id`, it.id, now.UTC()).Scan(&tenant, &sessionID); err != nil {
				return err
			}
			return audit(ctx, tx, tenant, sessionID, "recording.purged", "allowed", map[string]any{"recording_id": it.id, "reason": "retention_expired"})
		})
		if err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}
