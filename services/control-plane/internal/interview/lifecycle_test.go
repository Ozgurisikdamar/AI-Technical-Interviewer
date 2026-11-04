package interview

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

func TestFullInterviewFollowsPlanAndHandsOffIdentifiersOnly(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	if s := h.session(id); s.State != flow.Calibration || s.TimelineEpoch == nil || *s.InputMode != "voice" {
		t.Fatalf("after start: %+v", s)
	}
	// Calibration speech is stored but never evidence.
	h.clock.Advance(time.Second)
	r, _, err := h.segment(id, lease, "candidate", 200, 900, "yes I can hear you")
	h.must(err)
	if r.EvidenceEligible || r.ActivationID != nil {
		t.Fatalf("calibration segment must not be evidence: %+v", r)
	}
	h.must(h.svc.CalibrationComplete(ctx, agent, &lease, id, h.session(id).StateVersion))

	var items []string
	for range 4 {
		if s := h.session(id); s.State != flow.QuestionActive {
			t.Fatalf("expected QUESTION_ACTIVE, got %s", s.State)
		}
		items = append(items, h.answerOpenQuestion(id, lease, "synthetic answer"))
	}
	if !slices.Equal(items, []string{"q1", "q1f1", "q2", "q3"}) {
		t.Fatalf("plan order not followed: %v", items)
	}
	s := h.session(id)
	if s.State != flow.InterviewCompleted || s.CompletedAt == nil {
		t.Fatalf("final answer must complete the interview automatically: %s", s.State)
	}
	if n := h.count(`SELECT count(*) FROM ati.question_activations WHERE session_id = $1 AND closed_at IS NULL`, id); n != 0 {
		t.Fatalf("open activations after completion: %d", n)
	}
	if n := h.count(`SELECT count(*) FROM ati.candidate_answers WHERE session_id = $1`, id); n != 4 {
		t.Fatalf("answers = %d, want 4", n)
	}

	// Outbox: one answer.finalized per item and one interview.completed, each matching its contract.
	rows, err := h.db.CP.Query(ctx, `SELECT event_type, payload FROM ati.outbox_events WHERE session_id = $1 ORDER BY seq`, id)
	h.must(err)
	var types []string
	for rows.Next() {
		var typ string
		var payload []byte
		h.must(rows.Scan(&typ, &payload))
		schemaPath := contracts.AnswerFinalizedPath
		if typ == "interview.completed" {
			schemaPath = contracts.InterviewCompletedPath
		}
		if err := schema.ValidateJSON(schemaPath, payload); err != nil {
			t.Fatalf("%s payload violates contract: %v", typ, err)
		}
		var p map[string]any
		h.must(json.Unmarshal(payload, &p))
		if _, leaked := p["text"]; leaked {
			t.Fatal("handoff event carries transcript text")
		}
		if typ == "answer.finalized" {
			flags, _ := p["integrity_flags"].([]any)
			if !slices.Contains(flags, any("synthetic_source")) {
				t.Fatalf("synthetic answers must carry synthetic_source: %v", flags)
			}
		}
		types = append(types, typ)
	}
	rows.Close()
	if want := []string{"answer.finalized", "answer.finalized", "answer.finalized", "answer.finalized", "interview.completed"}; !slices.Equal(types, want) {
		t.Fatalf("outbox = %v", types)
	}

	// INV-7: one event per step, gapless versions, chain steps attributed to the system.
	rows, err = h.db.CP.Query(ctx, `SELECT state_version, transition_id, actor FROM ati.session_events WHERE session_id = $1 ORDER BY state_version`, id)
	h.must(err)
	var version int64
	var transitions []string
	for rows.Next() {
		var v int64
		var tr, actor string
		h.must(rows.Scan(&v, &tr, &actor))
		version++
		if v != version+1 {
			t.Fatalf("state versions not gapless: got %d after %d", v, version)
		}
		if (tr == "T08" || tr == "T09" || tr == "T10" || tr == "T11") && actor != "system" {
			t.Fatalf("chain step %s attributed to %s", tr, actor)
		}
		transitions = append(transitions, tr)
	}
	rows.Close()
	want := []string{"T01", "T02", "T04", "T05", "T06", "T07", "T08", "T09", "T06", "T07", "T10", "T06", "T07", "T10", "T06", "T07", "T11"}
	if !slices.Equal(transitions, want) {
		t.Fatalf("transitions = %v\nwant          %v", transitions, want)
	}
	if s.StateVersion != int64(len(want))+1 {
		t.Fatalf("state_version %d != 1 + steps %d", s.StateVersion, len(want))
	}
	if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action LIKE 'transition:%'`, id); n != len(want) {
		t.Fatalf("audit entries %d != steps %d", n, len(want))
	}
}

func TestCandidateButtonPathWaitsForFlushOrDeadline(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, key, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "part one")
	h.must(err)
	v := h.session(id).StateVersion

	if _, err := h.svc.RequestAnswerCompletion(ctx, candidate, id, v-1, act); !errors.Is(err, ErrStaleStateVersion) {
		t.Fatalf("stale request must be rejected: %v", err)
	}
	var conflict *ConflictError
	if _, err := h.svc.RequestAnswerCompletion(ctx, candidate, id, v, "act_000000000000000000000"); !errors.As(err, &conflict) {
		t.Fatalf("request for another activation must be rejected: %v", err)
	}
	if _, err := h.svc.RequestAnswerCompletion(ctx, candidate, id, v, act); err != nil {
		t.Fatal(err)
	}
	if s := h.session(id); s.State != flow.CandidateSpeaking {
		t.Fatalf("a request must not finalize by itself: %s", s.State)
	}
	// The agent confirms its flush; the answer is finalized with the candidate as cause.
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key}, "candidate_request"))
	var cause string
	var missing bool
	h.must(h.db.CP.QueryRow(ctx, `SELECT finalization_cause, missing_segments FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1'`, id).Scan(&cause, &missing))
	if cause != "candidate_request" || missing {
		t.Fatalf("cause=%s missing=%v", cause, missing)
	}

	// Next item (follow-up): the agent never confirms, the deadline sweeper finalizes with missing segments.
	act2, item2 := h.openActivation(id)
	if item2 != "q1f1" {
		t.Fatalf("expected the follow-up, got %s", item2)
	}
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act2, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, _, err = h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "vacuum")
	h.must(err)
	_, err = h.svc.RequestAnswerCompletion(ctx, candidate, id, h.session(id).StateVersion, act2)
	h.must(err)
	if n, _ := h.svc.SweepCompletionDeadlines(ctx); n != 0 {
		t.Fatalf("sweeper finalized before the deadline")
	}
	h.clock.Advance(9 * time.Second)
	if n, err := h.svc.SweepCompletionDeadlines(ctx); err != nil || n != 1 {
		t.Fatalf("sweeper: n=%d err=%v", n, err)
	}
	var flags []string
	h.must(h.db.CP.QueryRow(ctx, `SELECT missing_segments, integrity_flags FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1f1'`, id).Scan(&missing, &flags))
	if !missing || !slices.Contains(flags, "flush_deadline_elapsed") {
		t.Fatalf("deadline finalization must be flagged: missing=%v flags=%v", missing, flags)
	}
	if h.count(`SELECT count(*) FROM ati.integrity_events WHERE session_id = $1 AND kind = 'flush_deadline_elapsed'`, id) != 1 {
		t.Fatal("deadline finalization must record an integrity event")
	}
}

func TestPauseResumeOpensNewActivationAndRejectsSpeechDuringPause(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act1, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act1, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, key1, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id)+10, "before the pause")
	h.must(err)
	if h.session(id).State != flow.CandidateSpeaking {
		t.Fatal("first candidate segment must enter CANDIDATE_SPEAKING")
	}
	h.clock.Advance(time.Second)
	v := h.session(id).StateVersion
	h.must(h.svc.Pause(ctx, candidate, nil, id, &v, "network"))
	s := h.session(id)
	if s.State != flow.Paused || *s.PausedFrom != "CANDIDATE_SPEAKING" {
		t.Fatalf("pause: %s %v", s.State, s.PausedFrom)
	}
	h.clock.Advance(2 * time.Second)
	pausedAt := h.timeline(id) - 1000
	var rejected *SegmentRejectedError
	if _, _, err := h.segment(id, lease, "candidate", pausedAt, pausedAt+5, "spoken while paused"); !errors.As(err, &rejected) {
		t.Fatalf("speech during a pause must be rejected, got %v", err)
	}
	if h.count(`SELECT count(*) FROM ati.integrity_events WHERE session_id = $1 AND kind = 'segment_in_pause'`, id) != 1 {
		t.Fatal("rejection must be recorded even though the request failed")
	}
	h.must(h.svc.Resume(ctx, candidate, id, h.session(id).StateVersion))
	act2, item := h.openActivation(id)
	if act2 == act1 || item != "q1" || h.session(id).State != flow.QuestionActive {
		t.Fatalf("resume must open a new activation of the same item: %s %s", act2, item)
	}
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act2, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, key2, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "after the pause")
	h.must(err)
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, h.session(id).StateVersion, act2, []string{key1, key2}, "agent_tool"))
	var segs []string
	h.must(h.db.CP.QueryRow(ctx, `SELECT segment_ids FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1'`, id).Scan(&segs))
	if len(segs) != 2 {
		t.Fatalf("answer must combine both activations' evidence, got %d segments", len(segs))
	}
	if h.count(`SELECT count(*) FROM ati.pause_intervals WHERE session_id = $1 AND ended_ms IS NOT NULL`, id) != 1 {
		t.Fatal("pause interval must be closed on resume")
	}
}

func TestPauseDuringCalibrationResumesToCalibration(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCalibration()
	v := h.session(id).StateVersion
	h.must(h.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
	h.must(h.svc.Resume(ctx, candidate, id, h.session(id).StateVersion))
	if s := h.session(id); s.State != flow.Calibration {
		t.Fatalf("resume from calibration pause: %s", s.State)
	}
}

func TestWithdrawalCancelsWithoutCompletionAndDeletesRoom(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	h.must(h.svc.Withdraw(ctx, candidate, id, h.session(id).StateVersion))
	s := h.session(id)
	if s.State != flow.Cancelled || *s.CancelReason != "withdrawn" {
		t.Fatalf("withdraw: %s %v", s.State, s.CancelReason)
	}
	if h.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1 AND event_type = 'interview.completed'`, id) != 0 {
		t.Fatal("INV-10: cancelled sessions must not emit interview.completed")
	}
	if !slices.Contains(h.effects.deletedRooms, id) || !slices.Contains(h.effects.stopRecordings, id) {
		t.Fatalf("withdrawal must delete the room and stop recording: %+v", h.effects)
	}
	if h.count(`SELECT count(*) FROM ati.agent_leases WHERE session_id = $1 AND ended_at IS NULL`, id) != 0 {
		t.Fatal("withdrawal must end the agent lease")
	}
	if _, _, err := h.segment(id, lease, "candidate", 1, 2, "late"); !errors.Is(err, ErrLeaseStale) {
		t.Fatalf("writes after cancellation must fail: %v", err)
	}
}

func TestPauseExpiryCancels(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCalibration()
	v := h.session(id).StateVersion
	h.must(h.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
	if n, _ := h.svc.ExpirePausedSessions(ctx); n != 0 {
		t.Fatal("expired too early")
	}
	h.clock.Advance(31 * time.Minute)
	if n, err := h.svc.ExpirePausedSessions(ctx); err != nil || n != 1 {
		t.Fatalf("expiry: n=%d err=%v", n, err)
	}
	if s := h.session(id); s.State != flow.Cancelled || *s.CancelReason != "expired" {
		t.Fatalf("expiry result: %s", s.State)
	}
}

func TestConsentRules(t *testing.T) {
	h := newHarness(t)
	c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "s", CandidateRef: "ref-consent", PlanID: "plan_demo_backend_en", PlanVersion: 1})
	h.must(err)
	_, err = h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	h.must(err)
	all := map[string]bool{"voice_interview_processing": true, "transcript_assessment_record": true, "ai_assisted_evaluation_with_human_review": true, "audio_recording": false}
	var verr *ValidationError
	if err := h.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2099-01-x", all); !errors.As(err, &verr) {
		t.Fatalf("decisions on another notice version must be rejected: %v", err)
	}
	partial := map[string]bool{"transcript_assessment_record": true}
	if err := h.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", partial); !errors.As(err, &verr) {
		t.Fatalf("every purpose needs a decision: %v", err)
	}
	declined := map[string]bool{"voice_interview_processing": true, "transcript_assessment_record": false, "ai_assisted_evaluation_with_human_review": true, "audio_recording": false}
	h.must(h.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", declined))
	s := h.session(c.SessionID)
	if s.State != flow.Cancelled || *s.CancelReason != "consent_declined" {
		t.Fatalf("declining a required purpose must cancel: %s", s.State)
	}
	if h.count(`SELECT count(*) FROM ati.consent_records WHERE session_id = $1`, c.SessionID) != 4 {
		t.Fatal("every decision must be recorded")
	}
	var refused *TransitionRefusedError
	if err := h.svc.Start(ctx, candidate, c.SessionID, s.StateVersion, "text"); !errors.As(err, &refused) {
		t.Fatalf("a cancelled interview cannot start: %v", err)
	}
}

func TestVoiceModeRequiresVoiceConsentButTextModeDoesNot(t *testing.T) {
	h := newHarness(t)
	id := h.toReady(false)
	var conflict *ConflictError
	if err := h.svc.Start(ctx, candidate, id, h.session(id).StateVersion, "voice"); !errors.As(err, &conflict) || conflict.Code != "voice_processing_not_consented" {
		t.Fatalf("voice without consent: %v", err)
	}
	h.must(h.svc.Start(ctx, candidate, id, h.session(id).StateVersion, "text"))
	if s := h.session(id); *s.InputMode != "text" {
		t.Fatalf("input mode %v", s.InputMode)
	}
}

func TestInterviewCreationRequiresExplicitRetention(t *testing.T) {
	h := newHarness(t)
	h.svc.settings.Retention.AssessmentOutputDays = 0
	if _, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "s", CandidateRef: "r", PlanID: "plan_demo_backend_en", PlanVersion: 1}); !errors.Is(err, ErrRetentionMissing) {
		t.Fatalf("missing retention must refuse creation: %v", err)
	}
}

func TestInvitationReExchangeRevokesEarlierSession(t *testing.T) {
	h := newHarness(t)
	c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "s", CandidateRef: "r-inv", PlanID: "plan_demo_backend_en", PlanVersion: 1})
	h.must(err)
	first, err := h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	h.must(err)
	second, err := h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	h.must(err)
	if _, err := h.svc.AuthenticateCandidate(ctx, first.CandidateToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("the earlier candidate session must be revoked")
	}
	if got, err := h.svc.AuthenticateCandidate(ctx, second.CandidateToken); err != nil || got != c.SessionID {
		t.Fatalf("new session: %v %v", got, err)
	}
	if h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1 AND transition_id = 'T01'`, c.SessionID) != 1 {
		t.Fatal("only the first exchange performs T01")
	}
	h.clock.Advance(73 * time.Hour)
	if _, err := h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expired invitation accepted: %v", err)
	}
	if _, err := h.svc.ExchangeInvitation(ctx, candidate, "wrong-token-wrong-token-wrong-token-xx"); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatal("unknown token accepted")
	}
}

// One session the sweeper cannot process (here a database error injected for it) must not stop
// the sweep: the others are processed and the failure is reported.
func TestSweepersContinuePastAFailingSession(t *testing.T) {
	h := newHarness(t)
	var ids []string
	for range 3 {
		id, _ := h.toCalibration()
		v := h.session(id).StateVersion
		h.must(h.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
		ids = append(ids, id)
	}
	_, err := h.db.Owner.Exec(ctx, `CREATE FUNCTION ati.test_fail_one() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.id = '`+ids[0]+`' THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER test_fail_one BEFORE UPDATE ON ati.interview_sessions FOR EACH ROW EXECUTE FUNCTION ati.test_fail_one();`)
	h.must(err)
	h.clock.Advance(31 * time.Minute)
	n, err := h.svc.ExpirePausedSessions(ctx)
	if n != 2 || err == nil || !strings.Contains(err.Error(), ids[0]) {
		t.Fatalf("the sweep must process the other sessions and report the failing one: n=%d err=%v", n, err)
	}
	for _, id := range ids[1:] {
		if s := h.session(id); s.State != flow.Cancelled {
			t.Fatalf("session %s: %s", id, s.State)
		}
	}
	if s := h.session(ids[0]); s.State != flow.Paused {
		t.Fatalf("the failing session is untouched: %s", s.State)
	}
}

// An exchange racing a revocation of the same invitation must never deadlock: both lock the
// session before the invitation. Either the exchange wins (and its candidate session is revoked)
// or the invitation is already revoked.
func TestExchangeAndRevocationDoNotDeadlock(t *testing.T) {
	h := newHarness(t)
	for i := range 25 {
		c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "stf_test", CandidateRef: fmt.Sprintf("ref-race-%d", i), PlanID: "plan_demo_backend_en", PlanVersion: 1})
		h.must(err)
		var wg sync.WaitGroup
		var exErr, revErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, exErr = h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken) }()
		go func() { defer wg.Done(); _, revErr = h.svc.RevokeInvitation(ctx, staff, c.SessionID, h.tenant) }()
		wg.Wait()
		if revErr != nil || (exErr != nil && !errors.Is(exErr, ErrInvitationInvalid)) {
			t.Fatalf("iteration %d: exchange=%v revoke=%v", i, exErr, revErr)
		}
		if n := h.count(`SELECT count(*) FROM ati.candidate_sessions WHERE session_id = $1 AND revoked_at IS NULL`, c.SessionID); n != 0 {
			t.Fatalf("iteration %d: a candidate session survived the revocation", i)
		}
	}
}
