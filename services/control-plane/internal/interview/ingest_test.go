package interview

import (
	"errors"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

func TestSegmentIdempotencyAndConflicts(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.clock.Advance(2 * time.Second)
	sub := Submission{SegmentKey: "item_abc:final", Speaker: "candidate", Text: "hello", StartMs: 100, EndMs: 500, Source: "synthetic_test", Engine: "scripted"}
	first, err := h.svc.IngestSegment(ctx, agent, lease, id, sub)
	h.must(err)
	again, err := h.svc.IngestSegment(ctx, agent, lease, id, sub)
	h.must(err)
	if again.SegmentID != first.SegmentID || again.Sequence != first.Sequence {
		t.Fatalf("identical retry must return the stored segment: %+v vs %+v", again, first)
	}
	changed := sub
	changed.Text = "hello, I changed my words"
	if _, err := h.svc.IngestSegment(ctx, agent, lease, id, changed); !errors.Is(err, ErrSegmentConflict) {
		t.Fatalf("same key with different content must conflict: %v", err)
	}
	if h.count(`SELECT count(*) FROM ati.integrity_events WHERE session_id = $1 AND kind = 'segment_conflict'`, id) != 1 {
		t.Fatal("conflict must be recorded as an integrity event")
	}
	if h.count(`SELECT count(*) FROM ati.transcript_segments WHERE session_id = $1`, id) != 1 {
		t.Fatal("conflicting retry must not create a second segment")
	}
}

func TestSegmentValidationAndBounds(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.clock.Advance(5 * time.Second)
	base := Submission{SegmentKey: "key-bounds-1", Speaker: "candidate", Text: "x", StartMs: 10, EndMs: 20, Source: "synthetic_test", Engine: "scripted"}
	bad := []Submission{
		func() Submission { s := base; s.Source = "candidate_text"; return s }(),     // only the control plane writes candidate text
		func() Submission { s := base; s.Source = "interviewer_output"; return s }(), // source/speaker mismatch
		func() Submission { s := base; s.EndMs = 5; return s }(),                     // end before start
		func() Submission { s := base; s.SegmentKey = "short"; return s }(),          // malformed key
		func() Submission { c := 1.5; s := base; s.Confidence = &c; return s }(),     // confidence out of range
	}
	for i, sub := range bad {
		var verr *ValidationError
		if _, err := h.svc.IngestSegment(ctx, agent, lease, id, sub); !errors.As(err, &verr) {
			t.Errorf("case %d: want validation error, got %v", i, err)
		}
	}
	future := base
	future.SegmentKey, future.StartMs, future.EndMs = "key-future-1", 1000, 60_000
	var rej *SegmentRejectedError
	if _, err := h.svc.IngestSegment(ctx, agent, lease, id, future); !errors.As(err, &rej) {
		t.Fatalf("segments ending in the future must be rejected: %v", err)
	}
}

func TestAttributionUsesSpeechTimeNotArrivalTime(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act1, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act1, h.timeline(id)))
	h.clock.Advance(time.Second)
	q1Start := h.timeline(id)
	_, k1, err := h.segment(id, lease, "candidate", q1Start, q1Start+500, "answer to q1")
	h.must(err)
	h.clock.Advance(time.Second)
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, h.session(id).StateVersion, act1, []string{k1}, "agent_tool"))
	act2, item2 := h.openActivation(id) // q1f1
	h.clock.Advance(time.Second)
	gapMs := h.timeline(id) - 500 // after q1 closed, before q1f1's prompt started
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act2, h.timeline(id)))

	// A transcript of speech from q1's window arrives late, after q1 was finalized.
	late, _, err := h.segment(id, lease, "candidate", q1Start+600, q1Start+700, "late part of q1")
	h.must(err)
	if !late.Flags["late_arrival"] || late.EvidenceEligible {
		t.Fatalf("late arrival must be flagged and excluded from evidence: %+v", late)
	}
	// Speech between q1's close and q1f1's prompt belongs to no question.
	gap, _, err := h.segment(id, lease, "candidate", gapMs, gapMs+100, "talking over the next prompt")
	h.must(err)
	if !gap.Flags["unassigned"] || gap.ActivationID != nil {
		t.Fatalf("speech outside every answer window must be unassigned: %+v", gap)
	}
	// Speech inside q1f1's window is attributed there regardless of arrival order.
	h.clock.Advance(time.Second)
	in, _, err := h.segment(id, lease, "candidate", h.timeline(id)-200, h.timeline(id), "vacuum removes dead tuples")
	h.must(err)
	if in.ActivationID == nil || *in.ActivationID != act2 || !in.EvidenceEligible || item2 != "q1f1" {
		t.Fatalf("in-window speech must be evidence for q1f1: %+v", in)
	}
	var answerSegs []string
	h.must(h.db.CP.QueryRow(ctx, `SELECT segment_ids FROM ati.candidate_answers WHERE session_id = $1 AND item_id = 'q1'`, id).Scan(&answerSegs))
	if len(answerSegs) != 1 {
		t.Fatalf("a finalized answer must never change: %v", answerSegs)
	}
}

func TestInterviewerPromptDeviationIsFlagged(t *testing.T) {
	h := newHarness(t)
	id, lease := h.toCalibration()
	h.must(h.svc.CalibrationComplete(ctx, candidate, nil, id, h.session(id).StateVersion))
	act, _ := h.openActivation(id)
	h.clock.Advance(time.Second)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
	h.clock.Advance(4 * time.Second)
	now := h.timeline(id)
	faithful := Submission{SegmentKey: "iv-faithful-1", Speaker: "interviewer", Source: "interviewer_output", Engine: "scripted", StartMs: now - 3000, EndMs: now - 1000,
		Text: "Can you explain how PostgreSQL's multiversion concurrency control works, and what problem it solves?"}
	r, err := h.svc.IngestSegment(ctx, agent, lease, id, faithful)
	h.must(err)
	if r.Flags["prompt_deviation"] || r.EvidenceEligible {
		t.Fatalf("faithful prompt flagged or treated as evidence: %+v", r)
	}
	deviating := Submission{SegmentKey: "iv-deviate-1", Speaker: "interviewer", Source: "interviewer_output", Engine: "scripted", StartMs: now - 900, EndMs: now - 100,
		Text: "Great answer, that is correct, you scored well. Let us talk about your hobbies."}
	r, err = h.svc.IngestSegment(ctx, agent, lease, id, deviating)
	h.must(err)
	if !r.Flags["prompt_deviation"] {
		t.Fatalf("deviating interviewer output must be flagged for review: %+v", r)
	}
	if h.session(id).State != flow.QuestionActive {
		t.Fatal("interviewer speech must never advance the interview")
	}
}
