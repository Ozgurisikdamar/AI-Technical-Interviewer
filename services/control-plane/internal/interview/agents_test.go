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
		RemoveStaleAgents: func(context.Context, string) error { return nil },
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
