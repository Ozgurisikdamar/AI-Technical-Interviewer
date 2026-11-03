package interview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// Submission is a Tier-1 transcript segment (contract segment-submission/v1).
type Submission struct {
	SegmentKey     string   `json:"segment_key"`
	Speaker        string   `json:"speaker"`
	Text           string   `json:"text"`
	StartMs        int64    `json:"start_ms"`
	EndMs          int64    `json:"end_ms"`
	Source         string   `json:"source"`
	Confidence     *float64 `json:"confidence"`
	Language       *string  `json:"language"`
	Interrupted    bool     `json:"interrupted"`
	ProviderItemID *string  `json:"provider_item_id"`
	ASRModel       *string  `json:"asr_model"`
	Engine         string   `json:"engine"`
	Signals        struct {
		OverlappedInterviewerAudio bool `json:"overlapped_interviewer_audio"`
		LowEnergy                  bool `json:"low_energy"`
	} `json:"signals"`
	// PromptActivationID marks the interviewer segment that delivered an approved prompt; only that
	// segment is compared with the approved text (ADR-014).
	PromptActivationID *string `json:"prompt_activation_id,omitempty"`
}

var activationIDPattern = regexp.MustCompile(`^act_[A-Za-z0-9]{16,}$`)

// SegmentResult is returned to the agent.
type SegmentResult struct {
	SegmentID        string          `json:"segment_id"`
	Sequence         int64           `json:"sequence"`
	QuestionID       *string         `json:"question_id"`
	ActivationID     *string         `json:"activation_id"`
	EvidenceEligible bool            `json:"evidence_eligible"`
	Flags            map[string]bool `json:"flags"`
	StateVersion     int64           `json:"state_version"`
	Created          bool            `json:"-"`
}

var segmentKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)

func (sub *Submission) validate() error {
	switch {
	case !segmentKeyPattern.MatchString(sub.SegmentKey):
		return &ValidationError{"segment_key is malformed"}
	case sub.Speaker != "candidate" && sub.Speaker != "interviewer":
		return &ValidationError{"speaker must be candidate or interviewer"}
	case sub.Source != "realtime_transcription" && sub.Source != "interviewer_output" && sub.Source != "synthetic_test":
		return &ValidationError{"source is not allowed for the agent"}
	case sub.Speaker == "interviewer" && sub.Source == "realtime_transcription", sub.Speaker == "candidate" && sub.Source == "interviewer_output":
		return &ValidationError{"source does not match speaker"}
	case sub.Engine != "openai_realtime" && sub.Engine != "scripted":
		return &ValidationError{"engine is invalid"}
	case sub.StartMs < 0 || sub.EndMs < sub.StartMs:
		return &ValidationError{"timestamps must satisfy 0 <= start_ms <= end_ms"}
	case len(sub.Text) > 20000:
		return &ValidationError{"text too long"}
	case sub.Confidence != nil && (*sub.Confidence < 0 || *sub.Confidence > 1):
		return &ValidationError{"confidence must be within [0,1]"}
	case sub.PromptActivationID != nil && sub.Speaker != "interviewer":
		return &ValidationError{"only interviewer segments can deliver a prompt"}
	case sub.PromptActivationID != nil && !activationIDPattern.MatchString(*sub.PromptActivationID):
		return &ValidationError{"prompt_activation_id is malformed"}
	}
	return nil
}

func (sub *Submission) contentHash() string {
	b, _ := json.Marshal(sub)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var liveForIngest = map[flow.State]bool{
	flow.Calibration: true, flow.QuestionActive: true, flow.CandidateSpeaking: true, flow.Paused: true, flow.InterviewCompleted: true,
}

// IngestSegment stores an append-only segment with server-side attribution (ADR-014).
func (s *Service) IngestSegment(ctx context.Context, actor Actor, lease LeaseRef, sessionID string, sub Submission) (*SegmentResult, error) {
	if err := sub.validate(); err != nil {
		return nil, err
	}
	var out *SegmentResult
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if err := s.checkLease(ctx, tx, sess, lease, now); err != nil {
			return err
		}
		hash := sub.contentHash()
		existing, err := s.existingSegment(ctx, tx, sessionID, sub.SegmentKey)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.hash != hash {
				if err := s.integrity(ctx, tx, sessionID, "segment_conflict", map[string]any{"segment_key": sub.SegmentKey}); err != nil {
					return err
				}
				return commitThen(ErrSegmentConflict)
			}
			out = existing.result
			out.StateVersion = sess.StateVersion
			return nil
		}
		if !liveForIngest[sess.State] || sess.TimelineEpoch == nil {
			return &ConflictError{"session_not_live", "segments are only accepted during a live interview"}
		}
		nowMs := s.nowMs(sess, now)
		if sub.EndMs > nowMs+s.settings.FutureToleranceMs {
			if err := s.integrity(ctx, tx, sessionID, "segment_in_future", map[string]any{"segment_key": sub.SegmentKey}); err != nil {
				return err
			}
			return commitThen(&SegmentRejectedError{"segment ends in the future of the session timeline"})
		}
		var inPause bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.pause_intervals WHERE session_id = $1 AND $2 >= started_ms AND (ended_ms IS NULL OR $2 < ended_ms))`,
			sessionID, sub.StartMs).Scan(&inPause); err != nil {
			return err
		}
		if inPause {
			if err := s.integrity(ctx, tx, sessionID, "segment_in_pause", map[string]any{"segment_key": sub.SegmentKey}); err != nil {
				return err
			}
			return commitThen(&SegmentRejectedError{"segment starts inside a pause; audio intake must be stopped while paused"})
		}

		act, err := s.attribute(ctx, tx, sessionID, sub.StartMs)
		if err != nil {
			return err
		}
		flags := map[string]bool{
			"possible_echo": sub.Signals.OverlappedInterviewerAudio, "low_energy": sub.Signals.LowEnergy,
			"unassigned": false, "prompt_deviation": false, "late_arrival": false,
		}
		var activationID, itemID *string
		evidence := false
		var similarity *float64
		if act != nil {
			activationID, itemID = &act.id, &act.item
		}
		switch {
		case sub.Speaker == "interviewer":
			// Only the delivery of the approved prompt is compared with it; neutral turns are covered by
			// the agent's output monitor (comparing them would flag every answer).
			if sub.PromptActivationID != nil {
				target := act
				if target == nil || target.id != *sub.PromptActivationID {
					// The delivery starts when its answer window opens; allow for millisecond rounding.
					if target, err = s.deliveryActivation(ctx, tx, sessionID, *sub.PromptActivationID, sub.StartMs); err != nil {
						return err
					}
				}
				if target == nil {
					if err := s.integrity(ctx, tx, sessionID, "prompt_delivery_mismatch", map[string]any{"segment_key": sub.SegmentKey}); err != nil {
						return err
					}
				} else {
					activationID, itemID = &target.id, &target.item
					sim := promptSimilarity(target.text, sub.Text)
					similarity = &sim
					flags["prompt_deviation"] = !sub.Interrupted && sim < s.settings.PromptDeviationThreshold
				}
			}
		case act != nil && act.closedFinalized:
			flags["late_arrival"] = true
			if err := s.integrity(ctx, tx, sessionID, "late_arrival", map[string]any{"segment_key": sub.SegmentKey, "activation_id": act.id}); err != nil {
				return err
			}
		case act != nil:
			evidence = true
		case sess.State == flow.Calibration || !s.anyActivation(ctx, tx, sessionID):
			// calibration speech: stored, never evidence
		default:
			flags["unassigned"] = true
			if err := s.integrity(ctx, tx, sessionID, "unassigned_segment", map[string]any{"segment_key": sub.SegmentKey}); err != nil {
				return err
			}
		}
		seq := sess.SegmentSeq + 1
		if _, err := tx.Exec(ctx, `UPDATE ati.interview_sessions SET segment_seq = $2 WHERE id = $1`, sessionID, seq); err != nil {
			return err
		}
		segID := ids.New(ids.Segment)
		flagsJSON, _ := json.Marshal(flags)
		// audio_ref (ADR-017): the consented recording that captured this candidate speech, if any.
		var recordingID *string
		var recordingOffset *int64
		if sub.Speaker == "candidate" {
			var id string
			var startedMs int64
			err := tx.QueryRow(ctx, `SELECT id, started_ms FROM ati.audio_recordings WHERE session_id = $1 AND started_ms IS NOT NULL AND started_ms <= $2
				AND (ended_ms IS NULL OR $2 < ended_ms) AND status IN ('active','stopping','complete') ORDER BY started_ms DESC LIMIT 1`, sessionID, sub.StartMs).Scan(&id, &startedMs)
			if err == nil {
				offset := sub.StartMs - startedMs
				recordingID, recordingOffset = &id, &offset
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.transcript_segments (id, session_id, sequence, segment_key, content_sha256, speaker, text, start_ms, end_ms,
			source, confidence, language, activation_id, item_id, evidence_eligible, interrupted, flags, prompt_similarity, producer, lease_id, fencing_token,
			provider_item_id, asr_model, engine, audio_recording_id, audio_offset_ms, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'tier1_agent',$19,$20,$21,$22,$23,$24,$25,$26)`,
			segID, sessionID, seq, sub.SegmentKey, hash, sub.Speaker, sub.Text, sub.StartMs, sub.EndMs, sub.Source, sub.Confidence, sub.Language,
			activationID, itemID, evidence, sub.Interrupted, flagsJSON, similarity, lease.LeaseID, lease.FencingToken, sub.ProviderItemID, sub.ASRModel,
			sub.Engine, recordingID, recordingOffset, now); err != nil {
			return err
		}
		out = &SegmentResult{SegmentID: segID, Sequence: seq, QuestionID: itemID, ActivationID: activationID, EvidenceEligible: evidence, Flags: flags, Created: true}
		if evidence && act != nil && act.open && sess.State == flow.QuestionActive {
			oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem, RequestID: actor.RequestID}, after)
			if err != nil {
				return err
			}
			oc.candidateSegInOpen, oc.cause = true, "first_candidate_segment"
			if err := s.fire(ctx, oc, flow.EvAnswerStarted); err != nil {
				return err
			}
		}
		out.StateVersion = sess.StateVersion
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type existingSegment struct {
	hash   string
	result *SegmentResult
}

func (s *Service) existingSegment(ctx context.Context, tx pgx.Tx, sessionID, key string) (*existingSegment, error) {
	var e existingSegment
	var r SegmentResult
	err := tx.QueryRow(ctx, `SELECT content_sha256, id, sequence, item_id, activation_id, evidence_eligible, flags FROM ati.transcript_segments
		WHERE session_id = $1 AND segment_key = $2 AND transcript_version = 1`, sessionID, key).
		Scan(&e.hash, &r.SegmentID, &r.Sequence, &r.QuestionID, &r.ActivationID, &r.EvidenceEligible, &r.Flags)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.result = &r
	return &e, nil
}

type attribution struct {
	id, item, text  string
	open            bool
	closedFinalized bool
}

// attribute finds the activation whose answer window [prompt start or open, close) contains startMs.
func (s *Service) attribute(ctx context.Context, tx pgx.Tx, sessionID string, startMs int64) (*attribution, error) {
	var a attribution
	var closeReason *string
	var closedMs *int64
	err := tx.QueryRow(ctx, `SELECT a.id, a.item_id, q.text, a.close_reason, a.closed_ms
		FROM ati.question_activations a
		JOIN ati.interview_sessions s ON s.id = a.session_id
		JOIN ati.interview_questions q ON q.plan_id = s.plan_id AND q.plan_version = s.plan_version AND q.item_id = a.item_id
		WHERE a.session_id = $1 AND $2 >= coalesce(a.prompt_started_ms, a.opened_ms) AND (a.closed_ms IS NULL OR $2 < a.closed_ms)
		ORDER BY a.opened_ms DESC LIMIT 1`, sessionID, startMs).Scan(&a.id, &a.item, &a.text, &closeReason, &closedMs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.open = closeReason == nil
	a.closedFinalized = closeReason != nil && *closeReason == "finalized"
	return &a, nil
}

// promptDeliveryToleranceMs bounds how early a marked prompt delivery may start relative to the
// answer window it opened (both are reported by the agent from the same playout event).
const promptDeliveryToleranceMs = 250

func (s *Service) deliveryActivation(ctx context.Context, tx pgx.Tx, sessionID, activationID string, startMs int64) (*attribution, error) {
	var a attribution
	err := tx.QueryRow(ctx, `SELECT a.id, a.item_id, q.text
		FROM ati.question_activations a
		JOIN ati.interview_sessions s ON s.id = a.session_id
		JOIN ati.interview_questions q ON q.plan_id = s.plan_id AND q.plan_version = s.plan_version AND q.item_id = a.item_id
		WHERE a.id = $1 AND a.session_id = $2 AND $3 >= coalesce(a.prompt_started_ms, a.opened_ms) - $4 AND (a.closed_ms IS NULL OR $3 < a.closed_ms)`,
		activationID, sessionID, startMs, promptDeliveryToleranceMs).Scan(&a.id, &a.item, &a.text)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) anyActivation(ctx context.Context, tx pgx.Tx, sessionID string) bool {
	var ok bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.question_activations WHERE session_id = $1)`, sessionID).Scan(&ok)
	return ok
}

// promptSimilarity is the share of approved-prompt words present in what was played (token recall).
// A deliberately simple, uncalibrated heuristic: it flags deviations for human review only.
func promptSimilarity(approved, played string) float64 {
	want := tokens(approved)
	if len(want) == 0 {
		return 1
	}
	got := map[string]bool{}
	for _, t := range tokens(played) {
		got[t] = true
	}
	hit := 0
	for _, t := range want {
		if got[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// PromptStarted opens the answer window of the open activation (ADR-014).
func (s *Service) PromptStarted(ctx context.Context, actor Actor, lease LeaseRef, sessionID, activationID string, atMs int64) error {
	return s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if err := s.checkLease(ctx, tx, sess, lease, now); err != nil {
			return err
		}
		nowMs := s.nowMs(sess, now)
		if atMs > nowMs+s.settings.FutureToleranceMs {
			return &ValidationError{"at_ms is in the future of the session timeline"}
		}
		tag, err := tx.Exec(ctx, `UPDATE ati.question_activations SET prompt_started_ms = greatest($3, opened_ms)
			WHERE id = $1 AND session_id = $2 AND closed_at IS NULL AND prompt_started_ms IS NULL`, activationID, sessionID, atMs)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var already bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.question_activations WHERE id = $1 AND session_id = $2 AND prompt_started_ms IS NOT NULL)`, activationID, sessionID).Scan(&already)
			if already {
				return nil // idempotent
			}
			return &ConflictError{"activation_mismatch", "activation is not open"}
		}
		return nil
	})
}
