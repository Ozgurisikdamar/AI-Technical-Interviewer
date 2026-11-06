package interview

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type recordedEffects struct {
	mu             sync.Mutex
	deletedRooms   []string
	stopRecordings []string
}

func (r *recordedEffects) DeleteRoom(_ context.Context, sessionID, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletedRooms = append(r.deletedRooms, sessionID)
}
func (r *recordedEffects) StopRecording(_ context.Context, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopRecordings = append(r.stopRecordings, sessionID)
}
func (r *recordedEffects) StartRecording(context.Context, string) {}

type harness struct {
	t       *testing.T
	db      *testdb.DB
	svc     *Service
	clock   *clock
	effects *recordedEffects
	tenant  string
	keySeq  atomic.Int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testdb.New(t)
	m, err := flow.Default()
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	eff := &recordedEffects{}
	svc, err := New(db.CP, m, Settings{
		CompletionDeadline: 8 * time.Second, PauseTimeout: 30 * time.Minute, InvitationTTL: 72 * time.Hour,
		CandidateSessionTTL: 4 * time.Hour, LeaseTTL: 15 * time.Second,
		Retention: config.Retention{TranscriptDays: 30, SessionEventsDays: 30, AssessmentOutputDays: 30},
		NoticeID:  "notice_default", NoticeVersion: "2026-11-draft.1", AccessCode: accesscode.Cheap,
	}, eff, slog.New(slog.NewJSONHandler(io.Discard, nil)), c.Now)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, db: db, svc: svc, clock: c, effects: eff, tenant: ids.New(ids.Tenant)}
	if _, err := db.Owner.Exec(context.Background(), `INSERT INTO ati.tenants (id, name) VALUES ($1, 'Tenant')`, h.tenant); err != nil {
		t.Fatal(err)
	}
	return h
}

var (
	ctx       = context.Background()
	candidate = Actor{Type: flow.ActorCandidate, ID: "cand", RequestID: "req-test-0001"}
	staff     = Actor{Type: flow.ActorSystem, ID: "stf_test"}
	agent     = Actor{Type: flow.ActorTier1, ID: "agent-1"}
)

func (h *harness) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

// session returns a fresh copy of the session row.
func (h *harness) session(id string) *sessionRow {
	h.t.Helper()
	s, err := h.svc.getSession(ctx, h.db.CP, id)
	h.must(err)
	return s
}

func (h *harness) openActivation(id string) (string, string) {
	h.t.Helper()
	var act, item string
	err := h.db.CP.QueryRow(ctx, `SELECT id, item_id FROM ati.question_activations WHERE session_id = $1 AND closed_at IS NULL`, id).Scan(&act, &item)
	if err != nil {
		h.t.Fatalf("no open activation: %v", err)
	}
	return act, item
}

// toReady creates an interview and drives it to READY with the given voice decision.
func (h *harness) toReady(voice bool) string {
	h.t.Helper()
	c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "stf_test", CandidateRef: ids.New("ref"), PlanID: "plan_demo_backend_en", PlanVersion: 1})
	h.must(err)
	ex, err := h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	h.must(err)
	if ex.State != flow.ConsentPending {
		h.t.Fatalf("after exchange: %s", ex.State)
	}
	h.must(h.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", map[string]bool{
		"voice_interview_processing": voice, "transcript_assessment_record": true,
		"ai_assisted_evaluation_with_human_review": true, "audio_recording": false,
	}))
	return c.SessionID
}

// toCalibration starts the interview and issues an agent lease.
func (h *harness) toCalibration() (string, LeaseRef) {
	h.t.Helper()
	id := h.toReady(true)
	h.must(h.svc.Start(ctx, candidate, id, h.session(id).StateVersion, "voice"))
	l, err := h.svc.IssueLease(ctx, id, "AJ_job1", "agent-AJ_job1")
	h.must(err)
	return id, LeaseRef{LeaseID: l.ID, FencingToken: l.FencingToken}
}

func (h *harness) nextKey() string { return fmt.Sprintf("key-%08d", h.keySeq.Add(1)) }

func (h *harness) segment(id string, lease LeaseRef, speaker string, startMs, endMs int64, text string) (*SegmentResult, string, error) {
	key := h.nextKey()
	source := "synthetic_test"
	sub := Submission{SegmentKey: key, Speaker: speaker, Text: text, StartMs: startMs, EndMs: endMs, Source: source, Engine: "scripted"}
	r, err := h.svc.IngestSegment(ctx, agent, lease, id, sub)
	return r, key, err
}

// timeline returns the current session-timeline millisecond.
func (h *harness) timeline(id string) int64 {
	return h.svc.nowMs(h.session(id), h.clock.Now())
}

// answerOpenQuestion: prompt starts, the candidate speaks, the agent finalizes via the tool path.
func (h *harness) answerOpenQuestion(id string, lease LeaseRef, text string) string {
	h.t.Helper()
	act, item := h.openActivation(id)
	h.clock.Advance(500 * time.Millisecond)
	_, err := h.svc.RenewLease(ctx, id, lease) // agent heartbeat
	h.must(err)
	h.must(h.svc.PromptStarted(ctx, agent, lease, id, act, h.timeline(id)))
	h.clock.Advance(2 * time.Second)
	start := h.timeline(id)
	h.clock.Advance(3 * time.Second)
	_, key, err := h.segment(id, lease, "candidate", start, h.timeline(id), text)
	h.must(err)
	h.must(h.svc.AgentAnswerComplete(ctx, agent, lease, id, h.session(id).StateVersion, act, []string{key}, "agent_tool"))
	return item
}

func (h *harness) count(sql string, args ...any) int {
	h.t.Helper()
	var n int
	h.must(h.db.CP.QueryRow(ctx, sql, args...).Scan(&n))
	return n
}
