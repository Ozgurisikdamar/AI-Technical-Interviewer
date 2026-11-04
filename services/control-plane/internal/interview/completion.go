package interview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

func (s *Service) openActivationID(ctx context.Context, tx pgx.Tx, sessionID string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM ati.question_activations WHERE session_id = $1 AND closed_at IS NULL`, sessionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// RequestAnswerCompletion records the candidate's "Finish answer" request (R01). The answer is
// finalized once the agent confirms its transcript flush, or by the deadline sweeper.
func (s *Service) RequestAnswerCompletion(ctx context.Context, actor Actor, sessionID string, expected int64, activationID string) (time.Time, error) {
	var deadline time.Time
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		req, _ := s.machine.Request("R01")
		allowed := false
		for _, st := range req.AllowedIn {
			allowed = allowed || st == sess.State
		}
		if !allowed {
			return &TransitionRefusedError{State: sess.State, Event: "answer_completion_requested", Err: flow.ErrNoTransition}
		}
		open, err := s.openActivationID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if open == "" || open != activationID {
			return &ConflictError{"activation_mismatch", "the request does not refer to the open question"}
		}
		now := s.now().UTC()
		deadline = now.Add(s.settings.CompletionDeadline)
		tag, err := tx.Exec(ctx, `INSERT INTO ati.completion_requests (session_id, activation_id, requested_by, requested_at, deadline)
			VALUES ($1,$2,'candidate',$3,$4) ON CONFLICT (activation_id) WHERE resolved_at IS NULL DO NOTHING`, sessionID, activationID, now, deadline)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if err := tx.QueryRow(ctx, `SELECT deadline FROM ati.completion_requests WHERE activation_id = $1 AND resolved_at IS NULL`, activationID).Scan(&deadline); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, sess.TenantID, actor, "answer.completion_requested", "interview_session", sessionID, "allowed", map[string]any{"activation_id": activationID})
	})
	return deadline, err
}

// AgentAnswerComplete finalizes the open activation's answer once every flushed segment is stored.
// cause is agent_tool (tool path: the agent resolved its own request) or candidate_request
// (button path: confirms the candidate's pending request). Replays after finalization are idempotent.
func (s *Service) AgentAnswerComplete(ctx context.Context, actor Actor, lease LeaseRef, sessionID string, expected int64, activationID string, manifest []string, cause string) error {
	if cause != "agent_tool" && cause != "candidate_request" {
		return &ValidationError{"cause must be agent_tool or candidate_request"}
	}
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.authorizeActor(ctx, tx, sess, actor, &lease); err != nil {
			return err
		}
		var finalized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.question_activations WHERE id = $1 AND session_id = $2 AND close_reason = 'finalized')`,
			activationID, sessionID).Scan(&finalized); err != nil {
			return err
		}
		if finalized {
			return nil // idempotent replay of an already finalized answer
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		open, err := s.openActivationID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if open == "" || open != activationID {
			return &ConflictError{"activation_mismatch", "the request does not refer to the open question"}
		}
		if len(manifest) > 0 {
			var found int
			if err := tx.QueryRow(ctx, `SELECT count(DISTINCT segment_key) FROM ati.transcript_segments WHERE session_id = $1 AND segment_key = ANY($2)`,
				sessionID, manifest).Scan(&found); err != nil {
				return err
			}
			if found != len(uniq(manifest)) {
				return fmt.Errorf("%w: %d of %d received", ErrManifestIncomplete, found, len(uniq(manifest)))
			}
		}
		now := s.now().UTC()
		switch cause {
		case "agent_tool":
			if _, err := tx.Exec(ctx, `INSERT INTO ati.completion_requests (session_id, activation_id, requested_by, requested_at, deadline)
				VALUES ($1,$2,'tier1_agent',$3,$3) ON CONFLICT (activation_id) WHERE resolved_at IS NULL DO NOTHING`, sessionID, activationID, now); err != nil {
				return err
			}
		case "candidate_request":
			var pending bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.completion_requests WHERE activation_id = $1 AND resolved_at IS NULL)`, activationID).Scan(&pending); err != nil {
				return err
			}
			if !pending {
				return &ConflictError{"no_pending_request", "there is no pending candidate request for this question"}
			}
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.completion = &completionInput{resolved: true, missing: false, cause: cause}
		oc.closeReason, oc.cause = "finalized", cause
		if err := s.fire(ctx, oc, flow.EvAnswerFinalized); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE ati.completion_requests SET resolved_at = $2, resolution = 'finalized' WHERE activation_id = $1 AND resolved_at IS NULL`, activationID, now)
		return err
	})
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// SweepCompletionDeadlines finalizes answers whose candidate request deadline elapsed without an
// agent flush confirmation, marking them missing_segments=true. Returns how many were finalized.
func (s *Service) SweepCompletionDeadlines(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT session_id, activation_id FROM ati.completion_requests WHERE resolved_at IS NULL AND deadline <= $1`, s.now().UTC())
	if err != nil {
		return 0, err
	}
	type due struct{ session, activation string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.session, &d.activation); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	n := 0
	var errs []error
	for _, d := range list {
		err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
			sess, err := s.lockSession(ctx, tx, d.session)
			if err != nil {
				return err
			}
			var stillDue bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.completion_requests WHERE activation_id = $1 AND resolved_at IS NULL AND deadline <= $2)`,
				d.activation, s.now().UTC()).Scan(&stillDue); err != nil || !stillDue {
				return err
			}
			open, err := s.openActivationID(ctx, tx, d.session)
			if err != nil {
				return err
			}
			if open != d.activation || sess.State != flow.CandidateSpeaking {
				_, err := tx.Exec(ctx, `UPDATE ati.completion_requests SET resolved_at = $2, resolution = 'cancelled' WHERE activation_id = $1 AND resolved_at IS NULL`, d.activation, s.now().UTC())
				return err
			}
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem}, after)
			if err != nil {
				return err
			}
			oc.completion = &completionInput{resolved: true, missing: true, cause: "candidate_request_deadline"}
			oc.closeReason, oc.cause = "finalized", "candidate_request_deadline"
			if err := s.fire(ctx, oc, flow.EvAnswerFinalized); err != nil {
				return err
			}
			if err := s.integrity(ctx, tx, d.session, "flush_deadline_elapsed", map[string]any{"activation_id": d.activation}); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE ati.completion_requests SET resolved_at = $2, resolution = 'finalized' WHERE activation_id = $1 AND resolved_at IS NULL`, d.activation, s.now().UTC())
			if err == nil {
				n++
			}
			return err
		})
		if err != nil {
			// One session's failure must not hold up the others; the sweeper reports it.
			errs = append(errs, fmt.Errorf("session %s: %w", d.session, err))
		}
	}
	return n, errors.Join(errs...)
}

// ExpirePausedSessions cancels sessions paused longer than the pause timeout (T26).
func (s *Service) ExpirePausedSessions(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM ati.interview_sessions WHERE state = 'PAUSED' AND paused_at <= $1`, s.now().UTC().Add(-s.settings.PauseTimeout))
	if err != nil {
		return 0, err
	}
	var list []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, id)
	}
	rows.Close()
	n := 0
	var errs []error
	for _, id := range list {
		err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
			sess, err := s.lockSession(ctx, tx, id)
			if err != nil {
				return err
			}
			if sess.State != flow.Paused {
				return nil
			}
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem}, after)
			if err != nil {
				return err
			}
			oc.cause = "pause_timeout"
			if err := s.fire(ctx, oc, flow.EvPauseExpired); err != nil {
				var refused *TransitionRefusedError
				if errors.As(err, &refused) {
					return nil // not yet elapsed by the guard's clock
				}
				return err
			}
			n++
			return nil
		})
		if err != nil {
			// One session's failure must not hold up the others; the sweeper reports it.
			errs = append(errs, fmt.Errorf("session %s: %w", id, err))
		}
	}
	return n, errors.Join(errs...)
}

// Lease is issued to a claiming Tier-1 agent.
type Lease struct {
	ID           string
	Grant        string
	FencingToken int64
	ExpiresAt    time.Time
}

// IssueLease ends any active lease for the session and issues a new one with a higher fencing token.
// The API layer verifies the claim (nonce, dispatch, participant) before calling this.
func (s *Service) IssueLease(ctx context.Context, sessionID, jobID, agentIdentity string) (*Lease, error) {
	out := &Lease{ID: ids.New(ids.Lease), Grant: ids.Token()}
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		info, _ := s.machine.StateInfo(sess.State)
		if info.Kind != flow.KindLive && !postInterview[sess.State] { // the closing remark may still be due
			return &ConflictError{"session_not_live", "no live interview to claim"}
		}
		now := s.now().UTC()
		if _, err := tx.Exec(ctx, `UPDATE ati.agent_leases SET ended_at = $2, end_reason = 'superseded' WHERE session_id = $1 AND ended_at IS NULL`, sessionID, now); err != nil {
			return err
		}
		out.FencingToken = sess.LeaseFencing + 1
		out.ExpiresAt = now.Add(s.settings.LeaseTTL)
		if _, err := tx.Exec(ctx, `UPDATE ati.interview_sessions SET lease_fencing = $2 WHERE id = $1`, sessionID, out.FencingToken); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.agent_leases (id, session_id, fencing_token, grant_hash, job_id, agent_identity, issued_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, out.ID, sessionID, out.FencingToken, ids.Hash(out.Grant), jobID, agentIdentity, now, out.ExpiresAt); err != nil {
			return err
		}
		return s.audit(ctx, tx, sess.TenantID, Actor{Type: flow.ActorTier1, ID: agentIdentity}, "lease.issue", "interview_session", sessionID, "allowed",
			map[string]any{"fencing_token": out.FencingToken, "job_id": jobID})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AuthenticateLease resolves a grant to its lease (any session); fencing is re-checked per operation.
func (s *Service) AuthenticateLease(ctx context.Context, sessionID, grant string, fencing int64) (*LeaseRef, error) {
	var ref LeaseRef
	err := s.pool.QueryRow(ctx, `SELECT id, fencing_token FROM ati.agent_leases WHERE grant_hash = $1 AND session_id = $2 AND ended_at IS NULL AND expires_at > $3`,
		ids.Hash(grant), sessionID, s.now().UTC()).Scan(&ref.LeaseID, &ref.FencingToken)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ref.FencingToken != fencing) {
		return nil, ErrLeaseStale
	}
	return &ref, err
}

// RenewLease extends the current lease.
func (s *Service) RenewLease(ctx context.Context, sessionID string, lease LeaseRef) (time.Time, error) {
	var expires time.Time
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if err := s.checkLease(ctx, tx, sess, lease, now); err != nil {
			return err
		}
		expires = now.Add(s.settings.LeaseTTL)
		_, err = tx.Exec(ctx, `UPDATE ati.agent_leases SET expires_at = $2 WHERE id = $1`, lease.LeaseID, expires)
		return err
	})
	return expires, err
}

// ReleaseLease ends the lease voluntarily (agent shutdown).
func (s *Service) ReleaseLease(ctx context.Context, sessionID string, lease LeaseRef) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.checkLease(ctx, tx, sess, lease, s.now().UTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ati.agent_leases SET ended_at = $2, end_reason = 'released' WHERE id = $1`, lease.LeaseID, s.now().UTC()); err != nil {
			return err
		}
		return s.closeRoomAfterInterview(ctx, tx, sess, after, "agent_released")
	})
}

// ExpireLeases ends expired leases and pauses live sessions that lost their agent (system actor).
func (s *Service) ExpireLeases(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT session_id FROM ati.agent_leases WHERE ended_at IS NULL AND expires_at <= $1`, s.now().UTC())
	if err != nil {
		return 0, err
	}
	var list []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, id)
	}
	rows.Close()
	paused := 0
	var errs []error
	for _, id := range list {
		err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
			sess, err := s.lockSession(ctx, tx, id)
			if err != nil {
				return err
			}
			now := s.now().UTC()
			tag, err := tx.Exec(ctx, `UPDATE ati.agent_leases SET ended_at = $2, end_reason = 'expired' WHERE session_id = $1 AND ended_at IS NULL AND expires_at <= $2`, id, now)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			switch sess.State {
			case flow.Calibration, flow.QuestionActive, flow.CandidateSpeaking:
				oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem}, after)
				if err != nil {
					return err
				}
				oc.pauseReason, oc.closeReason, oc.cause = "agent_unavailable", "paused", "agent_unavailable"
				if err := s.fire(ctx, oc, flow.EvPause); err != nil {
					return err
				}
				paused++
				return nil
			}
			return s.closeRoomAfterInterview(ctx, tx, sess, after, "agent_lease_expired")
		})
		if err != nil {
			// One session's failure must not hold up the others; the sweeper reports it.
			errs = append(errs, fmt.Errorf("session %s: %w", id, err))
		}
	}
	return paused, errors.Join(errs...)
}
