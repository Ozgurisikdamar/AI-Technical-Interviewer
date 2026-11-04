package interview

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

// Revoked is the outcome of an invitation revocation.
type Revoked struct {
	Invitations       int64 `json:"invitations"`
	CandidateSessions int64 `json:"candidate_sessions"`
	Paused            bool  `json:"paused"`
	RoomDeleted       bool  `json:"room_deleted"`
}

// RevokeInvitation ends every way into an interview for whoever holds its invitation (ADR-016):
// the link can no longer be exchanged and any candidate session cookie stops working. A live
// interview is paused (access_revoked) and its room deleted at once, because self-hosted LiveKit
// cannot revoke a connected participant's token; nobody can resume it, so the pause expires to
// CANCELLED as usual. The state-machine contract has no staff cancellation, so other states are left as they
// are. Repeating the request changes nothing.
func (s *Service) RevokeInvitation(ctx context.Context, actor Actor, sessionID, tenantID string) (*Revoked, error) {
	out := &Revoked{}
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if sess.TenantID != tenantID {
			return ErrNotFound
		}
		now := s.now().UTC()
		tag, err := tx.Exec(ctx, `UPDATE ati.invitations SET revoked_at = $2 WHERE session_id = $1 AND revoked_at IS NULL`, sessionID, now)
		if err != nil {
			return err
		}
		out.Invitations = tag.RowsAffected()
		if tag, err = tx.Exec(ctx, `UPDATE ati.candidate_sessions SET revoked_at = $2 WHERE session_id = $1 AND revoked_at IS NULL`, sessionID, now); err != nil {
			return err
		}
		out.CandidateSessions = tag.RowsAffected()
		if out.Invitations+out.CandidateSessions == 0 {
			return s.audit(ctx, tx, sess.TenantID, actor, "invitation.revoke", "interview_session", sessionID, "allowed", map[string]any{"already_revoked": true})
		}
		switch sess.State {
		case flow.Calibration, flow.QuestionActive, flow.CandidateSpeaking:
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem, RequestID: actor.RequestID}, after)
			if err != nil {
				return err
			}
			oc.pauseReason, oc.closeReason, oc.cause = "access_revoked", "paused", "access_revoked"
			if err := s.fire(ctx, oc, flow.EvPause); err != nil {
				return err
			}
			out.Paused = true
		}
		switch sess.State {
		case flow.Calibration, flow.QuestionActive, flow.CandidateSpeaking, flow.Paused:
			id, room := sess.ID, sess.RoomName
			*after = append(*after, func(c context.Context) { s.effects.DeleteRoom(c, id, room) })
			out.RoomDeleted = true
			if err := s.audit(ctx, tx, sess.TenantID, Actor{Type: flow.ActorSystem, RequestID: actor.RequestID}, "media.room_deleted", "interview_session", id, "allowed",
				map[string]any{"reason": "access_revoked"}); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, sess.TenantID, actor, "invitation.revoke", "interview_session", sessionID, "allowed", map[string]any{
			"invitations": out.Invitations, "candidate_sessions": out.CandidateSessions, "paused": out.Paused, "room_deleted": out.RoomDeleted,
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
