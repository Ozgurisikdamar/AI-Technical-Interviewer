package tier1ctx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

const sentinel = "SENTINEL-CANDIDATE-WORDS-7731"

type env struct {
	db      *testdb.DB
	svc     *interview.Service
	builder *tier1ctx.Builder
	tenant  string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testdb.New(t)
	m, err := flow.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := interview.New(db.CP, m, interview.Settings{
		CompletionDeadline: 8 * time.Second, PauseTimeout: 30 * time.Minute, InvitationTTL: time.Hour, CandidateSessionTTL: time.Hour, LeaseTTL: time.Hour,
		Retention: config.Retention{TranscriptDays: 30, SessionEventsDays: 30, AssessmentOutputDays: 30},
		NoticeID:  "notice_default", NoticeVersion: "2026-11-draft.1", AccessCode: accesscode.Cheap,
	}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctxPool, err := store.Open(context.Background(), db.URLFor("ati_tier1_ctx"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctxPool.Close)
	e := &env{db: db, svc: svc, builder: tier1ctx.New(ctxPool, nil), tenant: ids.New(ids.Tenant)}
	if _, err := db.Owner.Exec(context.Background(), `INSERT INTO ati.tenants (id, name) VALUES ($1, 'T')`, e.tenant); err != nil {
		t.Fatal(err)
	}
	return e
}

var (
	bg        = context.Background()
	candidate = interview.Actor{Type: flow.ActorCandidate, ID: "c", RequestID: "req-tier1ctx-01"}
	agent     = interview.Actor{Type: flow.ActorTier1, ID: "a"}
)

func (e *env) live(t *testing.T) (string, interview.LeaseRef) {
	t.Helper()
	c, err := e.svc.CreateInterview(bg, interview.Actor{Type: flow.ActorSystem}, interview.CreateRequest{TenantID: e.tenant, StaffID: "s", CandidateRef: ids.New("r"), PlanID: "plan_demo_backend_en", PlanVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ExchangeInvitation(bg, candidate, c.InvitationToken); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SubmitConsent(bg, candidate, c.SessionID, "notice_default", "2026-11-draft.1", map[string]bool{
		"voice_interview_processing": true, "transcript_assessment_record": true, "ai_assisted_evaluation_with_human_review": true, "audio_recording": false}); err != nil {
		t.Fatal(err)
	}
	ctxv, err := e.builderVersion(c.SessionID)
	if err == nil {
		t.Fatalf("context must not exist before start: %+v", ctxv)
	}
	if err := e.svc.Start(bg, candidate, c.SessionID, 3, "voice"); err != nil {
		t.Fatal(err)
	}
	l, err := e.svc.IssueLease(bg, c.SessionID, "job", "agent-job")
	if err != nil {
		t.Fatal(err)
	}
	return c.SessionID, interview.LeaseRef{LeaseID: l.ID, FencingToken: l.FencingToken}
}

func (e *env) builderVersion(id string) (*tier1ctx.Context, error) { return e.builder.Build(bg, id) }

// timelineNow returns the current position on the session timeline from the context's epoch.
func timelineNow(c *tier1ctx.Context) int64 { return time.Now().UnixMilli() - *c.Timeline.EpochUnixMs }

func TestContextIsAllowlistedAndValid(t *testing.T) {
	e := setup(t)
	id, lease := e.live(t)
	c, err := e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "CALIBRATION" || c.ActivePrompt != nil || c.InputMode != "voice" || c.Timeline.EpochUnixMs == nil {
		t.Fatalf("calibration context: %+v", c)
	}
	if err := e.svc.CalibrationComplete(bg, candidate, nil, id, c.StateVersion); err != nil {
		t.Fatal(err)
	}
	c, err = e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.ActivePrompt == nil || c.ActivePrompt.ItemRef != "q1" || !strings.Contains(c.ActivePrompt.Text, "multiversion concurrency control") {
		t.Fatalf("active prompt: %+v", c.ActivePrompt)
	}
	// Candidate speaks a sentinel; it must never appear in the Tier-1 context.
	if err := e.svc.PromptStarted(bg, agent, lease, id, c.ActivePrompt.ActivationID, timelineNow(c)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	at := timelineNow(c)
	if _, err := e.svc.IngestSegment(bg, agent, lease, id, interview.Submission{SegmentKey: "sentinel-seg-1", Speaker: "candidate", Text: sentinel, StartMs: at - 10, EndMs: at, Source: "synthetic_test", Engine: "scripted"}); err != nil {
		t.Fatal(err)
	}
	c, err = e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AgentAnswerComplete(bg, agent, lease, id, c.StateVersion, c.ActivePrompt.ActivationID, []string{"sentinel-seg-1"}, "agent_tool"); err != nil {
		t.Fatal(err)
	}
	c, err = e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), sentinel) {
		t.Fatal("candidate words leaked into the Tier-1 context")
	}
	if regexp.MustCompile(`(?i)rubric|score|evaluat|review`).Match(raw) {
		t.Fatalf("forbidden concept in context: %s", raw)
	}
	if len(c.CompletedPrompts) != 1 || c.CompletedPrompts[0].ItemRef != "q1" || c.ActivePrompt.ItemRef != "q1f1" {
		t.Fatalf("progression: %+v %+v", c.CompletedPrompts, c.ActivePrompt)
	}
}

func TestPendingCandidateRequestIsVisibleToTheAgent(t *testing.T) {
	e := setup(t)
	id, lease := e.live(t)
	if err := e.svc.CalibrationComplete(bg, candidate, nil, id, 4); err != nil {
		t.Fatal(err)
	}
	c, _ := e.builder.Build(bg, id)
	if err := e.svc.PromptStarted(bg, agent, lease, id, c.ActivePrompt.ActivationID, timelineNow(c)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	at := timelineNow(c)
	if _, err := e.svc.IngestSegment(bg, agent, lease, id, interview.Submission{SegmentKey: "pending-seg-1", Speaker: "candidate", Text: "x", StartMs: at - 10, EndMs: at, Source: "synthetic_test", Engine: "scripted"}); err != nil {
		t.Fatal(err)
	}
	c, _ = e.builder.Build(bg, id)
	if _, err := e.svc.RequestAnswerCompletion(bg, candidate, id, c.StateVersion, c.ActivePrompt.ActivationID); err != nil {
		t.Fatal(err)
	}
	c, err := e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.PendingCompletionRequest == nil || c.PendingCompletionRequest.ActivationID != c.ActivePrompt.ActivationID {
		t.Fatalf("pending request not visible: %+v", c.PendingCompletionRequest)
	}
}

func TestPostInterviewStatesAreCollapsed(t *testing.T) {
	e := setup(t)
	id, _ := e.live(t)
	// Simulate a later-phase state directly (owner bypasses the flow engine for this check only).
	if _, err := e.db.Owner.Exec(bg, `UPDATE ati.interview_sessions SET state = 'HUMAN_REVIEW' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	c, err := e.builder.Build(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "INTERVIEW_COMPLETED" {
		t.Fatalf("Tier-1 must not learn review outcomes, got %s", c.State)
	}
}

func TestUnknownSessionHasNoContext(t *testing.T) {
	e := setup(t)
	if _, err := e.builder.Build(bg, "ses_0000000000000000000000"); !errors.Is(err, tier1ctx.ErrNotAvailable) {
		t.Fatalf("want ErrNotAvailable, got %v", err)
	}
}

func TestTier1ViewsExposeOnlyAllowlistedColumns(t *testing.T) {
	e := setup(t)
	rows, err := e.db.Owner.Query(bg, `SELECT c.table_name, c.column_name FROM information_schema.columns c
		JOIN information_schema.role_table_grants g ON g.table_schema = c.table_schema AND g.table_name = c.table_name
		WHERE g.grantee = 'ati_tier1_ctx' AND c.table_schema = 'ati'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	forbidden := regexp.MustCompile(`(?i)rubric|score|evaluat|review|candidate_ref|candidate_id|external_ref|tenant|token|grant|text_raw|flags|consent`)
	n := 0
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		n++
		if !strings.HasPrefix(table, "tier1_") || forbidden.MatchString(column) {
			t.Errorf("ati_tier1_ctx can read %s.%s", table, column)
		}
	}
	if n == 0 {
		t.Fatal("no Tier-1 view columns found")
	}
}

var _ = store.Open
