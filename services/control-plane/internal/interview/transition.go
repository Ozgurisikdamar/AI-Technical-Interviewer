package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// implementedGuards and implementedEffects must cover the contract (checked by New).
var implementedGuards = []string{
	"invitation_valid", "required_purposes_decided", "any_required_purpose_declined", "notice_version_pinned",
	"retention_policy_configured", "calibration_confirmed", "candidate_segment_in_open_activation",
	"completion_request_resolved", "approved_follow_up_pending", "next_plan_item_exists", "plan_exhausted",
	"paused_from_question", "paused_from_calibration", "pause_timeout_elapsed",
}

var implementedEffects = []string{
	"record_consent_decisions", "set_timeline_epoch", "open_activation_first_item", "open_activation_follow_up",
	"open_activation_next_item", "open_activation_same_item", "close_activation", "finalize_answer",
	"emit_answer_finalized", "emit_interview_completed", "record_paused_from", "end_pause",
	"cancel_completion_request", "start_recording", "stop_recording", "delete_room",
}

type consentInput struct {
	complete           bool
	allRequiredGranted bool
	noticeMatches      bool
	decisions          []consentDecision
}

type consentDecision struct {
	purpose       Purpose
	granted       bool
	retentionDays *int
}

type completionInput struct {
	resolved bool
	missing  bool
	cause    string // agent_tool | candidate_request | candidate_request_deadline
}

type step struct {
	tr    flow.Transition
	actor flow.Actor
}

// opCtx carries one operation's transaction, inputs and outputs.
type opCtx struct {
	tx    pgx.Tx
	sess  *sessionRow
	plan  *Plan
	actor Actor
	cause string
	now   time.Time
	after *[]func(context.Context)

	invitationValid    bool
	consent            *consentInput
	candidateSegInOpen bool
	completion         *completionInput
	inputMode          string
	pauseReason        string
	closeReason        string

	startVersion      int64
	steps             []step
	lastFinalizedItem string
	answerID          string
	openedActivation  string
}

func (s *Service) newOp(ctx context.Context, tx pgx.Tx, sess *sessionRow, actor Actor, after *[]func(context.Context)) (*opCtx, error) {
	plan, err := s.plans.load(ctx, tx, sess.PlanID, sess.PlanVersion)
	if err != nil {
		return nil, err
	}
	return &opCtx{tx: tx, sess: sess, plan: plan, actor: actor, now: s.now().UTC(), after: after, startVersion: sess.StateVersion}, nil
}

// fire resolves event for the acting party, applies it and any system chain, then persists.
func (s *Service) fire(ctx context.Context, oc *opCtx, event flow.Event) error {
	from := oc.sess.State
	tr, err := s.machine.Resolve(from, event, oc.actor.Type, s.guardFunc(ctx, oc))
	if err != nil {
		return &TransitionRefusedError{State: from, Event: event, Err: err}
	}
	if err := s.execute(ctx, oc, tr, oc.actor.Type); err != nil {
		return err
	}
	for i := 0; ; i++ {
		info, _ := s.machine.StateInfo(oc.sess.State)
		if !info.Transient {
			break
		}
		if i > 4 {
			return fmt.Errorf("chain from %s did not terminate", oc.sess.State)
		}
		next, err := s.machine.Chain(oc.sess.State, s.guardFunc(ctx, oc))
		if err != nil {
			return fmt.Errorf("contract chain from %s failed: %w", oc.sess.State, err)
		}
		if err := s.execute(ctx, oc, next, flow.ActorSystem); err != nil {
			return err
		}
	}
	return s.persist(ctx, oc)
}

func (s *Service) guardFunc(ctx context.Context, oc *opCtx) flow.GuardFunc {
	return func(g string, t flow.Transition) (bool, error) {
		switch g {
		case "invitation_valid":
			return oc.invitationValid, nil
		case "required_purposes_decided":
			return oc.consent != nil && oc.consent.complete && oc.consent.allRequiredGranted, nil
		case "any_required_purpose_declined":
			return oc.consent != nil && oc.consent.complete && !oc.consent.allRequiredGranted, nil
		case "notice_version_pinned":
			return oc.consent != nil && oc.consent.noticeMatches, nil
		case "retention_policy_configured":
			return retentionSnapshotOK(oc.sess.Retention), nil
		case "calibration_confirmed":
			if oc.actor.Type == flow.ActorCandidate {
				return true, nil
			}
			var ok bool
			err := oc.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.transcript_segments WHERE session_id = $1 AND speaker = 'candidate'
				AND activation_id IS NULL AND NOT evidence_eligible AND length(trim(text)) > 0)`, oc.sess.ID).Scan(&ok)
			return ok, err
		case "candidate_segment_in_open_activation":
			return oc.candidateSegInOpen, nil
		case "completion_request_resolved":
			return oc.completion != nil && oc.completion.resolved, nil
		case "approved_follow_up_pending":
			return oc.plan.FollowUpPending(oc.lastFinalizedItem), nil
		case "next_plan_item_exists":
			return oc.plan.NextQuestion(oc.lastFinalizedItem), nil
		case "plan_exhausted":
			return oc.plan.Exhausted(oc.lastFinalizedItem), nil
		case "paused_from_question":
			return oc.sess.PausedFrom != nil && (*oc.sess.PausedFrom == string(flow.QuestionActive) || *oc.sess.PausedFrom == string(flow.CandidateSpeaking)), nil
		case "paused_from_calibration":
			return oc.sess.PausedFrom != nil && *oc.sess.PausedFrom == string(flow.Calibration), nil
		case "pause_timeout_elapsed":
			return oc.sess.PausedAt != nil && oc.now.Sub(*oc.sess.PausedAt) >= s.settings.PauseTimeout, nil
		}
		return false, fmt.Errorf("guard %s (transition %s) not implemented", g, t.ID)
	}
}

func retentionSnapshotOK(raw json.RawMessage) bool {
	var r struct {
		TranscriptDays       int `json:"transcript_days"`
		SessionEventsDays    int `json:"session_events_days"`
		AssessmentOutputDays int `json:"assessment_output_days"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return false
	}
	return r.TranscriptDays > 0 && r.SessionEventsDays > 0 && r.AssessmentOutputDays > 0
}

var cancelReasons = map[flow.Event]string{
	flow.EvConsentDeclined: "consent_declined",
	flow.EvWithdraw:        "withdrawn",
	flow.EvPauseExpired:    "expired",
}

func (s *Service) execute(ctx context.Context, oc *opCtx, tr flow.Transition, actor flow.Actor) error {
	nowMs := s.nowMs(oc.sess, oc.now)
	for _, e := range tr.Effects {
		var err error
		switch e {
		case "record_consent_decisions":
			err = s.recordConsent(ctx, oc)
		case "set_timeline_epoch":
			epoch := oc.now
			oc.sess.TimelineEpoch = &epoch
			mode := oc.inputMode
			oc.sess.InputMode = &mode
			nowMs = 0
		case "open_activation_first_item":
			err = s.openActivation(ctx, oc, oc.plan.First(), nowMs)
		case "open_activation_follow_up", "open_activation_next_item":
			next := oc.plan.Next(oc.lastFinalizedItem)
			if next == nil {
				return fmt.Errorf("%s: no item after %s", tr.ID, oc.lastFinalizedItem)
			}
			err = s.openActivation(ctx, oc, next, nowMs)
		case "open_activation_same_item":
			var item string
			if err := oc.tx.QueryRow(ctx, `SELECT item_id FROM ati.question_activations WHERE session_id = $1 AND close_reason = 'paused'
				ORDER BY closed_at DESC LIMIT 1`, oc.sess.ID).Scan(&item); err != nil {
				return fmt.Errorf("resume: no paused activation: %w", err)
			}
			it, ok := oc.plan.Item(item)
			if !ok {
				return fmt.Errorf("resume: unknown item %s", item)
			}
			err = s.openActivation(ctx, oc, it, nowMs)
		case "close_activation":
			reason := oc.closeReason
			if reason == "" {
				return fmt.Errorf("%s: close reason missing", tr.ID)
			}
			var item string
			err = oc.tx.QueryRow(ctx, `UPDATE ati.question_activations SET closed_at = $2, closed_ms = greatest($3, opened_ms), close_reason = $4
				WHERE session_id = $1 AND closed_at IS NULL RETURNING item_id`, oc.sess.ID, oc.now, nowMs, reason).Scan(&item)
			if err == nil && reason == "finalized" {
				oc.lastFinalizedItem = item
			}
		case "finalize_answer":
			err = s.finalizeAnswer(ctx, oc, actor)
		case "emit_answer_finalized":
			err = s.emitAnswerFinalized(ctx, oc)
		case "emit_interview_completed":
			err = s.emitInterviewCompleted(ctx, oc)
		case "record_paused_from":
			from := string(tr.From)
			at := oc.now
			oc.sess.PausedFrom, oc.sess.PausedAt = &from, &at
			reason := oc.pauseReason
			if reason == "" {
				reason = "candidate_request"
			}
			_, err = oc.tx.Exec(ctx, `INSERT INTO ati.pause_intervals (session_id, started_ms, reason) VALUES ($1,$2,$3)`, oc.sess.ID, nowMs, reason)
		case "end_pause":
			oc.sess.PausedFrom, oc.sess.PausedAt = nil, nil
			_, err = oc.tx.Exec(ctx, `UPDATE ati.pause_intervals SET ended_ms = greatest($2, started_ms) WHERE session_id = $1 AND ended_ms IS NULL`, oc.sess.ID, nowMs)
		case "cancel_completion_request":
			_, err = oc.tx.Exec(ctx, `UPDATE ati.completion_requests SET resolved_at = $2, resolution = 'cancelled' WHERE session_id = $1 AND resolved_at IS NULL`, oc.sess.ID, oc.now)
		case "start_recording":
			// Only an attempt: the recorder applies the fail-closed policy (ADR-017).
			id := oc.sess.ID
			*oc.after = append(*oc.after, func(c context.Context) { s.effects.StartRecording(c, id) })
		case "stop_recording":
			id := oc.sess.ID
			*oc.after = append(*oc.after, func(c context.Context) { s.effects.StopRecording(c, id) })
		case "delete_room":
			id, room := oc.sess.ID, oc.sess.RoomName
			*oc.after = append(*oc.after, func(c context.Context) { s.effects.DeleteRoom(c, id, room) })
		default:
			return fmt.Errorf("effect %s not implemented", e)
		}
		if err != nil {
			return fmt.Errorf("%s effect %s: %w", tr.ID, e, err)
		}
	}
	if reason, ok := cancelReasons[tr.Event]; ok && tr.To == flow.Cancelled {
		at := oc.now
		oc.sess.CancelReason, oc.sess.CancelledAt = &reason, &at
		if _, err := oc.tx.Exec(ctx, `UPDATE ati.agent_leases SET ended_at = $2, end_reason = 'session_ended' WHERE session_id = $1 AND ended_at IS NULL`, oc.sess.ID, oc.now); err != nil {
			return err
		}
	}
	if tr.To == flow.InterviewCompleted {
		at := oc.now
		oc.sess.CompletedAt = &at
	}
	oc.sess.State = tr.To
	oc.steps = append(oc.steps, step{tr: tr, actor: actor})
	return nil
}

func (s *Service) openActivation(ctx context.Context, oc *opCtx, it *Item, nowMs int64) error {
	var attempt int
	if err := oc.tx.QueryRow(ctx, `SELECT coalesce(max(attempt), 0) + 1 FROM ati.question_activations WHERE session_id = $1 AND item_id = $2`, oc.sess.ID, it.ID).Scan(&attempt); err != nil {
		return err
	}
	id := ids.New(ids.Activation)
	var parent *string
	if it.ParentID != "" {
		parent = &it.ParentID
	}
	if _, err := oc.tx.Exec(ctx, `INSERT INTO ati.question_activations (id, session_id, item_id, parent_item_id, kind, attempt, opened_at, opened_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, oc.sess.ID, it.ID, parent, it.Kind, attempt, oc.now, nowMs); err != nil {
		return err
	}
	oc.openedActivation = id
	return nil
}

func (s *Service) recordConsent(ctx context.Context, oc *opCtx) error {
	if oc.consent == nil {
		return fmt.Errorf("consent decisions missing")
	}
	for _, d := range oc.consent.decisions {
		if _, err := oc.tx.Exec(ctx, `INSERT INTO ati.consent_records (id, session_id, notice_id, notice_version, purpose_id, required, granted, basis, retention_days, decided_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, ids.New(ids.Consent), oc.sess.ID, oc.sess.NoticeID, oc.sess.NoticeVersion,
			d.purpose.PurposeID, d.purpose.Required, d.granted, d.purpose.Basis, d.retentionDays, oc.now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finalizeAnswer(ctx context.Context, oc *opCtx, actor flow.Actor) error {
	if oc.completion == nil {
		return fmt.Errorf("finalize without a resolved completion request")
	}
	item, ok := oc.plan.Item(oc.lastFinalizedItem)
	if !ok {
		return fmt.Errorf("finalize: unknown item %q", oc.lastFinalizedItem)
	}
	rows, err := oc.tx.Query(ctx, `SELECT s.id, s.sequence, s.source, s.flags
		FROM ati.transcript_segments s JOIN ati.question_activations a ON a.id = s.activation_id
		WHERE s.session_id = $1 AND a.item_id = $2 AND s.speaker = 'candidate' AND s.evidence_eligible
		ORDER BY s.start_ms, s.sequence`, oc.sess.ID, item.ID)
	if err != nil {
		return err
	}
	var segIDs []string
	var lastSeq *int64
	flags := map[string]bool{}
	for rows.Next() {
		var id, source string
		var seq int64
		var fl map[string]bool
		if err := rows.Scan(&id, &seq, &source, &fl); err != nil {
			rows.Close()
			return err
		}
		segIDs = append(segIDs, id)
		if lastSeq == nil || seq > *lastSeq {
			v := seq
			lastSeq = &v
		}
		if source == "synthetic_test" {
			flags["synthetic_source"] = true
		}
		for _, f := range []string{"possible_echo", "low_energy", "late_arrival"} {
			if fl[f] {
				flags[f] = true
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var deviation, unassigned bool
	if err := oc.tx.QueryRow(ctx, `SELECT
			EXISTS (SELECT 1 FROM ati.transcript_segments s JOIN ati.question_activations a ON a.id = s.activation_id
				WHERE s.session_id = $1 AND a.item_id = $2 AND s.speaker = 'interviewer' AND (s.flags->>'prompt_deviation')::boolean),
			EXISTS (SELECT 1 FROM ati.transcript_segments WHERE session_id = $1 AND (flags->>'unassigned')::boolean)`,
		oc.sess.ID, item.ID).Scan(&deviation, &unassigned); err != nil {
		return err
	}
	if deviation {
		flags["prompt_deviation"] = true
	}
	if unassigned {
		flags["unassigned_segments_in_session"] = true
	}
	if oc.completion.cause == "candidate_request_deadline" {
		flags["flush_deadline_elapsed"] = true
	}
	flagList := make([]string, 0, len(flags))
	for f := range flags {
		flagList = append(flagList, f)
	}
	sort.Strings(flagList)
	if segIDs == nil {
		segIDs = []string{}
	}
	var parent *string
	if item.ParentID != "" {
		parent = &item.ParentID
	}
	oc.answerID = ids.New(ids.Answer)
	finalizedBy := "system"
	if actor == flow.ActorTier1 {
		finalizedBy = "tier1_agent"
	}
	_, err = oc.tx.Exec(ctx, `INSERT INTO ati.candidate_answers (id, session_id, item_id, parent_item_id, answer_version, transcript_version,
		segment_ids, last_sequence, missing_segments, integrity_flags, finalization_cause, finalized_by, finalized_at)
		VALUES ($1,$2,$3,$4,1,1,$5,$6,$7,$8,$9,$10,$11)`, oc.answerID, oc.sess.ID, item.ID, parent, segIDs, lastSeq,
		oc.completion.missing, flagList, oc.completion.cause, finalizedBy, oc.now)
	return err
}

func (s *Service) emitAnswerFinalized(ctx context.Context, oc *opCtx) error {
	var a struct {
		ItemID, Cause string
		Parent        *string
		SegIDs        []string
		LastSeq       *int64
		Missing       bool
		Flags         []string
	}
	if err := oc.tx.QueryRow(ctx, `SELECT item_id, parent_item_id, segment_ids, last_sequence, missing_segments, integrity_flags, finalization_cause
		FROM ati.candidate_answers WHERE id = $1`, oc.answerID).Scan(&a.ItemID, &a.Parent, &a.SegIDs, &a.LastSeq, &a.Missing, &a.Flags, &a.Cause); err != nil {
		return err
	}
	item, _ := oc.plan.Item(a.ItemID)
	var rubric any
	if item.RubricID != nil && item.RubricVersion != nil {
		rubric = map[string]any{"rubric_id": *item.RubricID, "version": *item.RubricVersion}
	}
	payload := map[string]any{
		"event_type": "answer.finalized", "event_version": 1, "event_id": ids.New(ids.Event),
		"occurred_at": oc.now.Format(time.RFC3339Nano), "tenant_id": oc.sess.TenantID, "session_id": oc.sess.ID,
		"answer_id": oc.answerID, "answer_version": 1,
		"plan":    map[string]any{"plan_id": oc.plan.ID, "version": oc.plan.Version},
		"item_id": a.ItemID, "parent_item_id": a.Parent, "rubric_ref": rubric, "transcript_version": 1,
		"segment_ids": a.SegIDs, "last_sequence": a.LastSeq, "missing_segments": a.Missing,
		"integrity_flags": a.Flags, "finalization_cause": a.Cause,
	}
	return s.insertOutbox(ctx, oc, "answer.finalized", contracts.AnswerFinalizedPath, payload)
}

func (s *Service) emitInterviewCompleted(ctx context.Context, oc *opCtx) error {
	var answers []string
	if err := oc.tx.QueryRow(ctx, `SELECT coalesce(array_agg(id ORDER BY finalized_at, id), '{}') FROM ati.candidate_answers WHERE session_id = $1`, oc.sess.ID).Scan(&answers); err != nil {
		return err
	}
	payload := map[string]any{
		"event_type": "interview.completed", "event_version": 1, "event_id": ids.New(ids.Event),
		"occurred_at": oc.now.Format(time.RFC3339Nano), "tenant_id": oc.sess.TenantID, "session_id": oc.sess.ID,
		"plan":       map[string]any{"plan_id": oc.plan.ID, "version": oc.plan.Version},
		"answer_ids": answers,
	}
	return s.insertOutbox(ctx, oc, "interview.completed", contracts.InterviewCompletedPath, payload)
}

// insertOutbox validates the payload against its contract before writing it (ADR-015).
func (s *Service) insertOutbox(ctx context.Context, oc *opCtx, eventType, schemaPath string, payload map[string]any) error {
	if err := schema.ValidateValue(schemaPath, payload); err != nil {
		return fmt.Errorf("%s payload violates its contract: %w", eventType, err)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = oc.tx.Exec(ctx, `INSERT INTO ati.outbox_events (id, tenant_id, session_id, event_type, event_version, payload) VALUES ($1,$2,$3,$4,1,$5)`,
		payload["event_id"], oc.sess.TenantID, oc.sess.ID, eventType, b)
	return err
}

// persist writes the final state once (transient states are never stored, INV-9) and one event
// plus one audit entry per step (INV-7).
func (s *Service) persist(ctx context.Context, oc *opCtx) error {
	n := int64(len(oc.steps))
	if n == 0 {
		return nil
	}
	if _, err := oc.tx.Exec(ctx, `UPDATE ati.interview_sessions SET state = $2, state_version = $3, input_mode = $4, paused_from = $5, paused_at = $6,
		timeline_epoch = $7, completed_at = $8, cancelled_at = $9, cancel_reason = $10, updated_at = $11 WHERE id = $1`,
		oc.sess.ID, string(oc.sess.State), oc.startVersion+n, oc.sess.InputMode, oc.sess.PausedFrom, oc.sess.PausedAt,
		oc.sess.TimelineEpoch, oc.sess.CompletedAt, oc.sess.CancelledAt, oc.sess.CancelReason, oc.now); err != nil {
		return err
	}
	for i, st := range oc.steps {
		version := oc.startVersion + int64(i) + 1
		details := map[string]any{}
		if oc.cause != "" {
			details["cause"] = oc.cause
		}
		if st.tr.HasEffect("finalize_answer") && oc.answerID != "" {
			details["answer_id"] = oc.answerID
		}
		b, _ := json.Marshal(details)
		if _, err := oc.tx.Exec(ctx, `INSERT INTO ati.session_events (session_id, state_version, transition_id, event, from_state, to_state, actor, cause, request_id, occurred_at, details)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11)`, oc.sess.ID, version, st.tr.ID, string(st.tr.Event),
			string(st.tr.From), string(st.tr.To), string(st.actor), oc.cause, oc.actor.RequestID, oc.now, b); err != nil {
			return err
		}
		auditActor := oc.actor
		if st.actor != oc.actor.Type {
			auditActor = Actor{Type: st.actor, RequestID: oc.actor.RequestID}
		}
		if err := s.audit(ctx, oc.tx, oc.sess.TenantID, auditActor, "transition:"+string(st.tr.Event), "interview_session", oc.sess.ID, "allowed",
			map[string]any{"transition": st.tr.ID, "from": st.tr.From, "to": st.tr.To, "state_version": version}); err != nil {
			return err
		}
	}
	oc.sess.StateVersion = oc.startVersion + n
	if s.observer != nil {
		steps := append([]step(nil), oc.steps...)
		*oc.after = append(*oc.after, func(context.Context) {
			for _, st := range steps {
				s.observer(st.tr.ID, st.actor)
			}
		})
	}
	return nil
}
