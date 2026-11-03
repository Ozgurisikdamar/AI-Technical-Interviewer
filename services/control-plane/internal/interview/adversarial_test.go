package interview

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

// Many concurrent finalization attempts for one answer: exactly one finalization (INV-4).
func TestConcurrentCompletionFinalizesExactlyOnce(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, key, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "answer")
	h.must(err)
	v := h.session(id).StateVersion
	var wg sync.WaitGroup
	errs := make(chan error, 25)
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key}, "agent_tool")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("replays of the same completion must be idempotent, got %v", err)
		}
	}
	if n := h.count(`SELECT count(*) FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1'`, id); n != 1 {
		t.Fatalf("answers for q1 = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1 AND event_type = 'answer.finalized'`, id); n != 1 {
		t.Fatalf("answer.finalized events = %d", n)
	}
}

// A pause/resume storm keeps the session consistent: gapless versions, one open activation.
func TestConcurrentPauseResumeStorm(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := h.session(id)
			if i%2 == 0 {
				v := s.StateVersion
				_ = h.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request")
			} else {
				_ = h.svc.Resume(ctx, candidate, id, s.StateVersion)
			}
		}(i)
	}
	wg.Wait()
	s := h.session(id)
	if s.State != flow.Paused && s.State != flow.QuestionActive {
		t.Fatalf("unexpected state after storm: %s", s.State)
	}
	if n := h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1`, id); int64(n) != s.StateVersion-1 {
		t.Fatalf("events %d vs version %d", n, s.StateVersion)
	}
	if n := h.count(`SELECT count(*) FROM (SELECT state_version - lag(state_version) OVER (ORDER BY state_version) AS d FROM ati.session_events WHERE session_id = $1) x WHERE d <> 1`, id); n != 0 {
		t.Fatalf("state versions have gaps")
	}
	open := h.count(`SELECT count(*) FROM ati.question_activations WHERE session_id = $1 AND closed_at IS NULL`, id)
	if (s.State == flow.QuestionActive) != (open == 1) || open > 1 {
		t.Fatalf("activation bookkeeping broken: state=%s open=%d", s.State, open)
	}
}

// Concurrent segments get unique, gapless sequence numbers.
func TestConcurrentIngestionAssignsGaplessSequences(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.clock.Advance(10 * time.Second)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sub := Submission{SegmentKey: fmt.Sprintf("conc-%04d", i), Speaker: "candidate", Text: "x", StartMs: int64(i * 10), EndMs: int64(i*10 + 5), Source: "synthetic_test", Engine: "scripted"}
			if _, err := h.svc.IngestSegment(ctx, agent, lease, id, sub); err != nil {
				t.Errorf("segment %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	var lo, hi, n int
	h.must(h.db.CP.QueryRow(ctx, `SELECT min(sequence), max(sequence), count(DISTINCT sequence) FROM ati.transcript_segments WHERE session_id = $1`, id).Scan(&lo, &hi, &n))
	if lo != 1 || hi != 50 || n != 50 {
		t.Fatalf("sequences min=%d max=%d distinct=%d", lo, hi, n)
	}
}

// The Tier-1 agent is treated as potentially compromised: whatever it calls, it cannot skip
// questions, finalize without candidate speech, act as the candidate, or reach another session.
func TestCompromisedAgentCannotEscalate(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	other, otherLease := h.toCalibration()

	var refused *TransitionRefusedError
	// Calibration cannot be confirmed by the agent before any candidate audio was received.
	if err := h.svc.CalibrationComplete(ctx, agent, &lease, id, h.session(id).StateVersion); !errors.As(err, &refused) {
		t.Fatalf("agent confirmed calibration without candidate audio: %v", err)
	}
	h.clock.Advance(time.Second)
	_, _, err := h.segment(id, lease, "candidate", 100, 300, "hello")
	h.must(err)
	h.must(h.svc.CalibrationComplete(ctx, agent, &lease, id, h.session(id).StateVersion))
	act, _ := h.openActivation(id)

	// Finalizing before the candidate said anything is refused (QUESTION_ACTIVE, no R01).
	if err := h.svc.AgentAnswerComplete(ctx, agent, lease, id, h.session(id).StateVersion, act, nil, "agent_tool"); !errors.As(err, &refused) {
		t.Fatalf("agent finalized an unanswered question: %v", err)
	}
	// The agent cannot resume, withdraw, consent or start on the candidate's behalf.
	for name, err := range map[string]error{
		"resume":   h.svc.Resume(ctx, agent, id, h.session(id).StateVersion),
		"withdraw": h.svc.Withdraw(ctx, agent, id, h.session(id).StateVersion),
		"start":    h.svc.Start(ctx, agent, id, h.session(id).StateVersion, "voice"),
	} {
		if !errors.As(err, &refused) {
			t.Errorf("agent %s: want refusal, got %v", name, err)
		}
	}
	// Its lease is useless for another session, and the other session's lease is useless here.
	if _, _, err := h.segment(other, lease, "candidate", 1, 2, "cross-session"); !errors.Is(err, ErrLeaseStale) {
		t.Fatalf("cross-session write with a foreign lease: %v", err)
	}
	if err := h.svc.PromptStarted(ctx, agent, otherLease, id, act, 1); !errors.Is(err, ErrLeaseStale) {
		t.Fatalf("foreign lease accepted: %v", err)
	}
	// A superseded lease (re-dispatch) is fenced off.
	newLease, err := h.svc.IssueLease(ctx, id, "AJ_job2", "agent-AJ_job2")
	h.must(err)
	if _, _, err := h.segment(id, lease, "candidate", 1, 2, "zombie"); !errors.Is(err, ErrLeaseStale) {
		t.Fatalf("superseded lease still writes: %v", err)
	}
	if newLease.FencingToken <= lease.FencingToken {
		t.Fatalf("fencing token must increase: %d -> %d", lease.FencingToken, newLease.FencingToken)
	}
	if h.session(id).State != flow.QuestionActive {
		t.Fatalf("no agent action may have advanced the interview: %s", h.session(id).State)
	}
}

func TestLeaseExpiryPausesTheInterview(t *testing.T) {
	h := newHarness(t)
	id, _ := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	h.clock.Advance(16 * time.Second)
	if n, err := h.svc.ExpireLeases(ctx); err != nil || n != 1 {
		t.Fatalf("expire leases: n=%d err=%v", n, err)
	}
	s := h.session(id)
	if s.State != flow.Paused {
		t.Fatalf("agent loss must pause the interview: %s", s.State)
	}
	var reason string
	h.must(h.db.CP.QueryRow(ctx, `SELECT reason FROM ati.pause_intervals WHERE session_id = $1`, id).Scan(&reason))
	if reason != "agent_unavailable" {
		t.Fatalf("pause reason %s", reason)
	}
	if h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1 AND transition_id = 'T12' AND actor = 'system'`, id) != 1 {
		t.Fatal("lease expiry pause must be a system transition")
	}
}

func TestStaleVersionsAreRejected(t *testing.T) {
	h := newHarness(t)
	id := h.toReady(true)
	v := h.session(id).StateVersion
	h.must(h.svc.Start(ctx, candidate, id, v, "voice"))
	if err := h.svc.Withdraw(ctx, candidate, id, v); !errors.Is(err, ErrStaleStateVersion) {
		t.Fatalf("a request based on an old version must be rejected: %v", err)
	}
}
