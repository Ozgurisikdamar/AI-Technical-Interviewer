package interview

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

func fakeOps() MediaOps {
	return MediaOps{
		RemoveStaleAgents: func(context.Context, string, string) error { return nil },
		Dispatch:          func(context.Context, string, string) (string, error) { return "AD_test", nil },
	}
}

func TestMissingAgentPausesAfterTheDispatchTimeout(t *testing.T) {
	h := newHarness(t)
	id := h.toReady(true)
	h.must(h.svc.Start(ctx, candidate, id, h.session(id).StateVersion, "voice"))
	created, err := h.svc.EnsureAgentDispatch(ctx, id, fakeOps())
	h.must(err)
	if !created {
		t.Fatal("first media request must dispatch an agent")
	}
	h.clock.Advance(29 * time.Second)
	if n, err := h.svc.SweepMissingAgents(ctx); err != nil || n != 0 {
		t.Fatalf("a dispatch inside the timeout must not pause: n=%d err=%v", n, err)
	}
	h.clock.Advance(2 * time.Second)
	if n, err := h.svc.SweepMissingAgents(ctx); err != nil || n != 1 {
		t.Fatalf("unclaimed dispatch past the timeout must pause: n=%d err=%v", n, err)
	}
	if s := h.session(id); s.State != flow.Paused {
		t.Fatalf("state %s", s.State)
	}
	if h.count(`SELECT count(*) FROM ati.pause_intervals WHERE session_id = $1 AND reason = 'agent_unavailable'`, id) != 1 {
		t.Fatal("pause reason must be agent_unavailable")
	}
	if h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1 AND cause = 'agent_missing' AND actor = 'system'`, id) != 1 {
		t.Fatal("the pause must be an audited system transition")
	}
	if h.count(`SELECT count(*) FROM ati.agent_dispatches WHERE session_id = $1 AND superseded_at IS NULL AND claimed_at IS NULL`, id) != 0 {
		t.Fatal("the stale dispatch must be superseded")
	}
	// Idempotent: a paused session is not swept again.
	if n, _ := h.svc.SweepMissingAgents(ctx); n != 0 {
		t.Fatal("swept twice")
	}
	// Resuming allows a fresh dispatch.
	h.must(h.svc.Resume(ctx, candidate, id, h.session(id).StateVersion))
	created, err = h.svc.EnsureAgentDispatch(ctx, id, fakeOps())
	h.must(err)
	if !created {
		t.Fatal("resume must be able to dispatch a replacement agent")
	}
}

func TestSessionWithALiveAgentIsNotSwept(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	for range 3 {
		h.clock.Advance(11 * time.Second)
		_, err := h.svc.RenewLease(ctx, id, lease)
		h.must(err)
	}
	if n, err := h.svc.SweepMissingAgents(ctx); err != nil || n != 0 {
		t.Fatalf("live lease must not be swept: n=%d err=%v", n, err)
	}
	if s := h.session(id); s.State != flow.Calibration {
		t.Fatalf("state %s", s.State)
	}
}

func TestRoomStaysForTheClosingRemarkAndIsDeletedWhenTheAgentLeaves(t *testing.T) {
	for _, how := range []string{"release", "left", "expired"} {
		t.Run(how, func(t *testing.T) {
			h := newHarness(t)
			id, lease := h.toCalibration()
			h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
			for i := 0; i < 10 && h.session(id).State != flow.InterviewCompleted; i++ {
				h.answerOpenQuestion(id, lease, "a synthetic answer")
			}
			if s := h.session(id); s.State != flow.InterviewCompleted {
				t.Fatalf("state %s", s.State)
			}
			if slices.Contains(h.effects.deletedRooms, id) {
				t.Fatal("the room must remain while the closing remark is spoken")
			}
			switch how {
			case "release":
				h.must(h.svc.ReleaseLease(ctx, id, lease))
			case "left":
				h.must(h.svc.AgentLeft(ctx, id, "agent-AJ_job1"))
			case "expired":
				h.clock.Advance(20 * time.Second)
				_, err := h.svc.ExpireLeases(ctx)
				h.must(err)
			}
			if !slices.Contains(h.effects.deletedRooms, id) {
				t.Fatal("the room must be deleted once the agent is gone")
			}
			if h.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'media.room_deleted'`, id) != 1 {
				t.Fatal("room deletion must be audited")
			}
			if s := h.session(id); s.State != flow.InterviewCompleted {
				t.Fatalf("leaving must not change the state: %s", s.State)
			}
		})
	}
}

func TestReleaseDuringALiveInterviewKeepsTheRoom(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.ReleaseLease(ctx, id, lease))
	if slices.Contains(h.effects.deletedRooms, id) {
		t.Fatal("a live interview's room must not be deleted when an agent releases")
	}
}

// The closing remark is spoken after INTERVIEW_COMPLETED. By the time its transcript arrives, the outbox
// relay may already have enqueued the evaluation (T14) and the Tier-2 worker routed the interview to review
// (T15): the agent's closing duties work in every post-interview state (ADR-022).
func TestTheClosingRemarkIsStoredAfterTheInterviewMovedOnToReview(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	for i := 0; i < 10 && h.session(id).State != flow.InterviewCompleted; i++ {
		h.answerOpenQuestion(id, lease, "a synthetic answer")
	}
	h.must(h.svc.EnqueueEvaluation(ctx, id))
	_, err := h.svc.CompleteEvaluation(ctx, Tier2Actor, id, "")
	h.must(err)
	if s := h.session(id); s.State != flow.HumanReview {
		t.Fatalf("state %s", s.State)
	}
	h.clock.Advance(2 * time.Second)
	_, err = h.svc.RenewLease(ctx, id, lease)
	h.must(err)
	now := h.timeline(id)
	closing := Submission{SegmentKey: h.nextKey(), Speaker: "interviewer", Source: "interviewer_output", Engine: "scripted", StartMs: now - 1500, EndMs: now - 100,
		Text: "Thank you. That was the last question."}
	if _, err := h.svc.IngestSegment(ctx, agent, lease, id, closing); err != nil {
		t.Fatalf("the closing remark must be stored after the interview moved on: %v", err)
	}
	if n := h.count(`SELECT count(*) FROM ati.transcript_segments WHERE session_id = $1 AND text = 'Thank you. That was the last question.'`, id); n != 1 {
		t.Fatalf("closing remark stored %d times", n)
	}
	h.must(h.svc.ReleaseLease(ctx, id, lease))
	if !slices.Contains(h.effects.deletedRooms, id) {
		t.Fatal("the room is deleted once the agent is gone, in review as after completion")
	}
	if s := h.session(id); s.State != flow.HumanReview {
		t.Fatalf("the agent leaving must not change the review state: %s", s.State)
	}
}
