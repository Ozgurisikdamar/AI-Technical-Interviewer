package interview

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// DispatchMetadata is the only data placed in LiveKit dispatch metadata: no grant, no secret
// that would be useful after the nonce is claimed (ADR-016).
type DispatchMetadata struct {
	SessionID  string `json:"session_id"`
	ClaimNonce string `json:"claim_nonce"`
}

// MediaOps are the media-layer calls the dispatch logic needs (implemented by the API layer).
type MediaOps struct {
	RemoveStaleAgents func(ctx context.Context, room string) error
	Dispatch          func(ctx context.Context, room, metadata string) (string, error)
}

const nonceTTL = 2 * time.Minute

// RoomFor returns the opaque room name and input mode of a session.
func (s *Service) RoomFor(ctx context.Context, sessionID string) (room string, state flow.State, inputMode string, err error) {
	sess, err := s.getSession(ctx, s.pool, sessionID)
	if err != nil {
		return "", "", "", err
	}
	mode := ""
	if sess.InputMode != nil {
		mode = *sess.InputMode
	}
	return sess.RoomName, sess.State, mode, nil
}

// SessionForRoom maps a LiveKit room to its session.
func (s *Service) SessionForRoom(ctx context.Context, room string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id FROM ati.interview_sessions WHERE room_name = $1`, room).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// EnsureAgentDispatch dispatches an agent unless the session has an active lease or a pending,
// unexpired dispatch. Runs under the session row lock, so concurrent joins never double-dispatch.
func (s *Service) EnsureAgentDispatch(ctx context.Context, sessionID string, ops MediaOps) (bool, error) {
	created := false
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT
				EXISTS (SELECT 1 FROM ati.agent_leases WHERE session_id = $1 AND ended_at IS NULL AND expires_at > $2)
			 OR EXISTS (SELECT 1 FROM ati.agent_dispatches WHERE session_id = $1 AND claimed_at IS NULL AND superseded_at IS NULL AND nonce_expires_at > $2)`,
			sessionID, now).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE ati.agent_dispatches SET superseded_at = $2 WHERE session_id = $1 AND superseded_at IS NULL AND claimed_at IS NULL`, sessionID, now); err != nil {
			return err
		}
		if err := ops.RemoveStaleAgents(ctx, sess.RoomName); err != nil {
			return err
		}
		nonce := ids.Token()
		meta, _ := json.Marshal(DispatchMetadata{SessionID: sessionID, ClaimNonce: nonce})
		dispatchID, err := ops.Dispatch(ctx, sess.RoomName, string(meta))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.agent_dispatches (session_id, livekit_dispatch_id, nonce_hash, nonce_expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`,
			sessionID, dispatchID, ids.Hash(nonce), now.Add(nonceTTL), now); err != nil {
			return err
		}
		created = true
		return s.audit(ctx, tx, sess.TenantID, Actor{Type: flow.ActorSystem}, "agent.dispatch", "interview_session", sessionID, "allowed", map[string]any{"dispatch_id": dispatchID})
	})
	return created, err
}

// ClaimDispatch exchanges a single-use dispatch nonce for a lease. verify must confirm through the
// media API that the dispatch and the claiming agent participant are real (ADR-016).
func (s *Service) ClaimDispatch(ctx context.Context, sessionID, nonce, jobID, agentIdentity string, verify func(ctx context.Context, room, dispatchID string) error) (*Lease, error) {
	var claimed bool
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		var id int64
		var dispatchID *string
		err = tx.QueryRow(ctx, `SELECT id, livekit_dispatch_id FROM ati.agent_dispatches WHERE session_id = $1 AND nonce_hash = $2
			AND claimed_at IS NULL AND superseded_at IS NULL AND nonce_expires_at > $3 FOR UPDATE`, sessionID, ids.Hash(nonce), now).Scan(&id, &dispatchID)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = s.audit(ctx, tx, sess.TenantID, Actor{Type: flow.ActorTier1, ID: agentIdentity}, "agent.claim", "interview_session", sessionID, "denied", map[string]any{"reason": "nonce invalid, used or expired"})
			return commitThen(ErrNonceInvalid)
		}
		if err != nil {
			return err
		}
		if dispatchID == nil {
			return commitThen(ErrNonceInvalid)
		}
		if err := verify(ctx, sess.RoomName, *dispatchID); err != nil {
			_ = s.audit(ctx, tx, sess.TenantID, Actor{Type: flow.ActorTier1, ID: agentIdentity}, "agent.claim", "interview_session", sessionID, "denied", map[string]any{"reason": err.Error()})
			return commitThen(ErrNonceInvalid)
		}
		_, err = tx.Exec(ctx, `UPDATE ati.agent_dispatches SET claimed_at = $2, claimed_job_id = $3 WHERE id = $1`, id, now, jobID)
		claimed = err == nil
		return err
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, ErrNonceInvalid
	}
	return s.IssueLease(ctx, sessionID, jobID, agentIdentity)
}

// AgentLeft ends the lease of a departed agent immediately and pauses a live interview.
func (s *Service) AgentLeft(ctx context.Context, sessionID, identity string) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE ati.agent_leases SET ended_at = $3, end_reason = 'expired' WHERE session_id = $1 AND agent_identity = $2 AND ended_at IS NULL`,
			sessionID, identity, s.now().UTC())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		switch sess.State {
		case flow.Calibration, flow.QuestionActive, flow.CandidateSpeaking:
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem}, after)
			if err != nil {
				return err
			}
			oc.pauseReason, oc.closeReason, oc.cause = "agent_unavailable", "paused", "agent_left"
			return s.fire(ctx, oc, flow.EvPause)
		}
		return nil
	})
}

// ActiveAgentIdentity returns the identity holding the current lease, if any.
func (s *Service) ActiveAgentIdentity(ctx context.Context, sessionID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT agent_identity FROM ati.agent_leases WHERE session_id = $1 AND ended_at IS NULL ORDER BY fencing_token DESC LIMIT 1`, sessionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// UsageReport is cumulative provider usage for the cost model (STACK section 5).
type UsageReport struct {
	Engine            string  `json:"engine"`
	Model             *string `json:"model"`
	InputAudioMs      int64   `json:"input_audio_ms"`
	OutputAudioMs     int64   `json:"output_audio_ms"`
	InputTextTokens   *int64  `json:"input_text_tokens"`
	OutputTextTokens  *int64  `json:"output_text_tokens"`
	InputAudioTokens  *int64  `json:"input_audio_tokens"`
	OutputAudioTokens *int64  `json:"output_audio_tokens"`
}

// RecordUsage upserts cumulative usage (monotonic: never decreases).
func (s *Service) RecordUsage(ctx context.Context, lease LeaseRef, sessionID string, u UsageReport) error {
	if u.Engine != "openai_realtime" && u.Engine != "scripted" {
		return &ValidationError{"unknown engine"}
	}
	if u.InputAudioMs < 0 || u.OutputAudioMs < 0 {
		return &ValidationError{"usage must be non-negative"}
	}
	return s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.checkLease(ctx, tx, sess, lease, s.now().UTC()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ati.session_usage AS u (session_id, engine, model, input_audio_ms, output_audio_ms, input_text_tokens, output_text_tokens, input_audio_tokens, output_audio_tokens, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (session_id) DO UPDATE SET engine = EXCLUDED.engine, model = EXCLUDED.model,
			  input_audio_ms = greatest(u.input_audio_ms, EXCLUDED.input_audio_ms), output_audio_ms = greatest(u.output_audio_ms, EXCLUDED.output_audio_ms),
			  input_text_tokens = greatest(u.input_text_tokens, EXCLUDED.input_text_tokens), output_text_tokens = greatest(u.output_text_tokens, EXCLUDED.output_text_tokens),
			  input_audio_tokens = greatest(u.input_audio_tokens, EXCLUDED.input_audio_tokens), output_audio_tokens = greatest(u.output_audio_tokens, EXCLUDED.output_audio_tokens),
			  updated_at = EXCLUDED.updated_at`,
			sessionID, u.Engine, u.Model, u.InputAudioMs, u.OutputAudioMs, u.InputTextTokens, u.OutputTextTokens, u.InputAudioTokens, u.OutputAudioTokens, s.now().UTC())
		return err
	})
}

var policyFlags = map[string]bool{"possible_score_disclosure": true, "possible_rubric_disclosure": true, "possible_prompt_disclosure": true}

// RecordPolicyFlag stores a Tier-1 output-monitor flag as an integrity event (review trigger only).
func (s *Service) RecordPolicyFlag(ctx context.Context, lease LeaseRef, sessionID, flag string, activationID *string) error {
	if !policyFlags[flag] {
		return &ValidationError{"unknown policy flag"}
	}
	return s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.checkLease(ctx, tx, sess, lease, s.now().UTC()); err != nil {
			return err
		}
		return s.integrity(ctx, tx, sessionID, "policy_flag", map[string]any{"flag": flag, "activation_id": activationID})
	})
}

// Observer receives committed transitions (metrics). Called after commit.
type Observer func(transitionID string, actor flow.Actor)

// SetObserver installs a transition observer.
func (s *Service) SetObserver(o Observer) { s.observer = o }
