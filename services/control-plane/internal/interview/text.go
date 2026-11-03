package interview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// MaxTextResponseLength bounds one text answer (characters; tier1-context/v1 untrusted_text).
const MaxTextResponseLength = 4000

// TextResponseResult is returned to the candidate (OpenAPI TextResponseAccepted).
type TextResponseResult struct {
	SegmentID    string     `json:"segment_id"`
	State        flow.State `json:"state"`
	StateVersion int64      `json:"state_version"`
}

// SubmitTextResponse stores a text-mode answer as a candidate segment written by the control plane
// itself (ADR-018): bound to the open activation, server timestamps, no lease, never via LiveKit.
// The Tier-1 agent receives it as untrusted user input through its context view.
func (s *Service) SubmitTextResponse(ctx context.Context, actor Actor, sessionID, activationID, text string) (*TextResponseResult, error) {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxTextResponseLength || !utf8.ValidString(text) {
		return nil, &ValidationError{"text must contain 1 to 4000 characters"}
	}
	var out *TextResponseResult
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.authorizeActor(ctx, tx, sess, actor, nil); err != nil {
			return err
		}
		if sess.InputMode == nil || *sess.InputMode != "text" {
			return &ConflictError{"input_mode_mismatch", "text responses are accepted only in text mode"}
		}
		if sess.State != flow.QuestionActive && sess.State != flow.CandidateSpeaking {
			return &ConflictError{"not_answering", "there is no open question to answer"}
		}
		open, err := s.openActivationID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if open == "" || open != activationID {
			return &ConflictError{"activation_mismatch", "the request does not refer to the open question"}
		}
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.completion_requests WHERE activation_id = $1 AND resolved_at IS NULL)`, open).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return &ConflictError{"completion_pending", "the answer is being closed"}
		}
		var itemID string
		if err := tx.QueryRow(ctx, `SELECT item_id FROM ati.question_activations WHERE id = $1`, open).Scan(&itemID); err != nil {
			return err
		}
		now := s.now().UTC()
		nowMs := s.nowMs(sess, now)
		segID := ids.New(ids.Segment)
		h := sha256.Sum256([]byte(text))
		flags, _ := json.Marshal(map[string]bool{"possible_echo": false, "low_energy": false, "unassigned": false, "prompt_deviation": false, "late_arrival": false})
		seq := sess.SegmentSeq + 1
		if _, err := tx.Exec(ctx, `UPDATE ati.interview_sessions SET segment_seq = $2 WHERE id = $1`, sessionID, seq); err != nil {
			return err
		}
		sess.SegmentSeq = seq
		if _, err := tx.Exec(ctx, `INSERT INTO ati.transcript_segments (id, session_id, sequence, segment_key, content_sha256, speaker, text, start_ms, end_ms,
			source, activation_id, item_id, evidence_eligible, flags, producer, created_at)
			VALUES ($1,$2,$3,$4,$5,'candidate',$6,$7,$7,'candidate_text',$8,$9,true,$10,'control_plane',$11)`,
			segID, sessionID, seq, "cp:text:"+segID, hex.EncodeToString(h[:]), text, nowMs, open, itemID, flags, now); err != nil {
			return err
		}
		// No text in the audit log: only identifiers and size.
		if err := s.audit(ctx, tx, sess.TenantID, actor, "text_response.submit", "interview_session", sessionID, "allowed",
			map[string]any{"segment_id": segID, "activation_id": open, "characters": utf8.RuneCountInString(text)}); err != nil {
			return err
		}
		if sess.State == flow.QuestionActive {
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem, RequestID: actor.RequestID}, after)
			if err != nil {
				return err
			}
			oc.candidateSegInOpen, oc.cause = true, "first_candidate_segment"
			if err := s.fire(ctx, oc, flow.EvAnswerStarted); err != nil {
				return err
			}
		}
		out = &TextResponseResult{SegmentID: segID, State: sess.State, StateVersion: sess.StateVersion}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
