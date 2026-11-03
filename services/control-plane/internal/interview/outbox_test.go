package interview

import (
	"testing"
	"time"
)

// ADR-015: the outbox holds an event only for a committed finalization. A refused finalization and
// one whose transaction rolls back after the event was written leave nothing for a relay to
// publish, and the answer can still be finalized exactly once afterwards.
func TestOutboxHoldsOnlyCommittedFinalizations(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
	h.clock.Advance(time.Second)
	_, key, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "answer")
	h.must(err)
	outbox := func() int { return h.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1`, id) }
	v := h.session(id).StateVersion

	refusals := map[string]func() error{
		"manifest names an unknown segment": func() error {
			return h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key, "key-never-received"}, "agent_tool")
		},
		"another activation": func() error {
			return h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, "act_000000000000000000other", []string{key}, "agent_tool")
		},
		"stale state version": func() error {
			return h.svc.AgentAnswerComplete(ctx, agent, lease, id, v-1, act, []string{key}, "agent_tool")
		},
		"stale lease": func() error {
			return h.svc.AgentAnswerComplete(ctx, agent, LeaseRef{LeaseID: lease.LeaseID, FencingToken: lease.FencingToken + 1}, id, v, act, []string{key}, "agent_tool")
		},
	}
	for name, call := range refusals {
		if err := call(); err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if n := outbox(); n != 0 {
			t.Fatalf("%s: %d outbox rows after a refused finalization", name, n)
		}
	}

	// Roll back the finalization at commit, after the outbox row was inserted in the transaction.
	for _, stmt := range []string{
		`CREATE FUNCTION ati.test_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$`,
		`CREATE CONSTRAINT TRIGGER test_fail_commit AFTER INSERT ON ati.outbox_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ati.test_fail_commit()`,
	} {
		if _, err := h.db.Owner.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key}, "agent_tool"); err == nil {
		t.Fatal("the injected commit failure was not reported")
	}
	if n := outbox(); n != 0 {
		t.Fatalf("%d outbox rows survived a rolled-back finalization", n)
	}
	if s := h.session(id); s.StateVersion != v || s.State != "CANDIDATE_SPEAKING" {
		t.Fatalf("the rollback must leave the session as it was: %s v%d", s.State, s.StateVersion)
	}
	if n := h.count(`SELECT count(*) FROM ati.candidate_answers WHERE session_id = $1`, id); n != 0 {
		t.Fatalf("%d answers survived the rollback", n)
	}

	if _, err := h.db.Owner.Exec(ctx, `DROP TRIGGER test_fail_commit ON ati.outbox_events`); err != nil {
		t.Fatal(err)
	}
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key}, "agent_tool"))
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, v, act, []string{key}, "agent_tool")) // replay
	if n := h.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1 AND event_type = 'answer.finalized'`, id); n != 1 {
		t.Fatalf("answer.finalized events after the retry = %d", n)
	}
}

// ADR-015: cancellation never emits a completion or finalization event, whatever its cause.
func TestCancellationsEmitNoHandoffEvents(t *testing.T) {
	cases := map[string]func(h *harness) string{
		"consent declined": func(h *harness) string {
			c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "stf_test", CandidateRef: "ref-decline", PlanID: "plan_demo_backend_en", PlanVersion: 1})
			h.must(err)
			_, err = h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
			h.must(err)
			h.must(h.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", map[string]bool{
				"voice_interview_processing": true, "transcript_assessment_record": false, "ai_assisted_evaluation_with_human_review": true, "audio_recording": false,
			}))
			return c.SessionID
		},
		"pause expired mid-question": func(h *harness) string {
			id, lease := h.toCalibration()
			h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
			act, _ := h.openActivation(id)
			h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
			h.clock.Advance(time.Second)
			_, _, err := h.segment(id, lease, "candidate", h.timeline(id), h.timeline(id), "partial answer")
			h.must(err)
			v := h.session(id).StateVersion
			h.must(h.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
			h.clock.Advance(31 * time.Minute)
			n, err := h.svc.ExpirePausedSessions(ctx)
			h.must(err)
			if n != 1 {
				h.t.Fatalf("expired %d sessions", n)
			}
			return id
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			id := run(h)
			if s := h.session(id); s.State != "CANCELLED" {
				t.Fatalf("state %s", s.State)
			}
			if n := h.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1`, id); n != 0 {
				t.Fatalf("%d outbox events for a cancelled interview", n)
			}
			if n := h.count(`SELECT count(*) FROM ati.candidate_answers WHERE session_id = $1`, id); n != 0 {
				t.Fatalf("a cancelled question produced %d answers", n)
			}
		})
	}
}
