package recording_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/recording"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

var (
	ctx       = context.Background()
	candidate = interview.Actor{Type: flow.ActorCandidate, ID: "cand", RequestID: "req-recording-01"}
	staff     = interview.Actor{Type: flow.ActorSystem, ID: "stf_test"}
	agent     = interview.Actor{Type: flow.ActorTier1, ID: "agent-AJ_rec"}
)

// effects forwards the state machine's recording effects to the recorder, like api.Effects.
type effects struct{ rec *recording.Recorder }

func (effects) DeleteRoom(context.Context, string, string)           {}
func (e effects) StartRecording(c context.Context, sessionID string) { e.rec.Start(c, sessionID) }
func (e effects) StopRecording(c context.Context, sessionID string)  { e.rec.Stop(c, sessionID) }

type env struct {
	t      *testing.T
	db     *testdb.DB
	svc    *interview.Service
	rec    *recording.Recorder
	media  *realtime.Fake
	egress *realtime.FakeEgress
	tenant string
}

type options struct {
	deployEnabled, deployAttested, tenantAllows, consent bool
	retentionDays                                        int
	inputMode                                            string
	noMic                                                bool
	egressTimeout                                        time.Duration
}

var allowed = options{deployEnabled: true, deployAttested: true, tenantAllows: true, consent: true, retentionDays: 30, inputMode: "voice"}

func newEnv(t *testing.T, o options) *env {
	t.Helper()
	db := testdb.New(t)
	m, err := flow.Default()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, media: realtime.NewFake(), egress: &realtime.FakeEgress{}, tenant: ids.New(ids.Tenant)}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	e.rec = recording.New(db.CP, e.media, e.egress, recording.Settings{Enabled: o.deployEnabled, EncryptionAttested: o.deployAttested, RetentionDays: o.retentionDays,
		EgressTimeout: o.egressTimeout}, log, nil)
	e.svc, err = interview.New(db.CP, m, interview.Settings{
		CompletionDeadline: 8 * time.Second, PauseTimeout: 30 * time.Minute, InvitationTTL: time.Hour, CandidateSessionTTL: time.Hour, LeaseTTL: time.Hour,
		Retention:        config.Retention{TranscriptDays: 30, SessionEventsDays: 30, AssessmentOutputDays: 30, AudioRecordingDays: o.retentionDays},
		RecordingEnabled: o.deployEnabled, RecordingAttested: o.deployAttested, NoticeID: "notice_default", NoticeVersion: "2026-11-draft.1",
	}, effects{rec: e.rec}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Owner.Exec(ctx, `INSERT INTO ati.tenants (id, name, recording_allowed) VALUES ($1, 'T', $2)`, e.tenant, o.tenantAllows); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) version(id string) int64 {
	v, err := e.svc.CandidateView(ctx, id, "test")
	e.must(err)
	return v.StateVersion
}

// toFirstQuestion runs an interview to its first open question (T05 starts the recording).
func (e *env) toFirstQuestion(o options) (string, string, interview.LeaseRef) {
	e.t.Helper()
	c, err := e.svc.CreateInterview(ctx, staff, interview.CreateRequest{TenantID: e.tenant, StaffID: "stf_test", CandidateRef: ids.New("ref"), PlanID: "plan_demo_backend_en", PlanVersion: 1})
	e.must(err)
	_, err = e.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	e.must(err)
	voice := o.inputMode == "voice"
	e.must(e.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", map[string]bool{
		"voice_interview_processing": voice, "transcript_assessment_record": true, "ai_assisted_evaluation_with_human_review": true, "audio_recording": o.consent,
	}))
	e.must(e.svc.Start(ctx, candidate, c.SessionID, e.version(c.SessionID), o.inputMode))
	room, _, _, err := e.svc.RoomFor(ctx, c.SessionID)
	e.must(err)
	e.media.Join(room, realtime.CandidateIdentity, false)
	if !o.noMic {
		e.media.PublishMic(room, realtime.CandidateIdentity, "TR_mic0001")
	}
	l, err := e.svc.IssueLease(ctx, c.SessionID, "AJ_rec", "agent-AJ_rec")
	e.must(err)
	e.must(e.svc.CalibrationComplete(ctx, candidate, nil, c.SessionID, e.version(c.SessionID)))
	return c.SessionID, room, interview.LeaseRef{LeaseID: l.ID, FencingToken: l.FencingToken}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.db.CP.QueryRow(ctx, sql, args...).Scan(&n))
	return n
}

func (e *env) skippedReasons(id string) []string {
	e.t.Helper()
	var raw []string
	rows, err := e.db.CP.Query(ctx, `SELECT jsonb_array_elements_text(details->'reasons') FROM ati.audit_logs WHERE subject_id = $1 AND action = 'recording.skipped'`, id)
	e.must(err)
	defer rows.Close()
	for rows.Next() {
		var r string
		e.must(rows.Scan(&r))
		raw = append(raw, r)
	}
	return raw
}

func TestEachPreconditionAloneDisablesRecording(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*options)
		reason string
	}{
		{"feature disabled", func(o *options) { o.deployEnabled = false }, recording.ReasonFeatureDisabled},
		{"tenant does not allow", func(o *options) { o.tenantAllows = false }, recording.ReasonTenantNotAllowed},
		{"candidate declined", func(o *options) { o.consent = false }, recording.ReasonNoConsent},
		{"no retention period", func(o *options) { o.retentionDays = 0 }, recording.ReasonNoRetention},
		{"encryption not attested", func(o *options) { o.deployAttested = false }, recording.ReasonEncryptionNotAttested},
		{"text mode", func(o *options) { o.inputMode = "text" }, recording.ReasonNotVoice},
		{"no microphone track", func(o *options) { o.noMic = true }, recording.ReasonNoMicrophone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := allowed
			tc.mutate(&o)
			e := newEnv(t, o)
			id, _, _ := e.toFirstQuestion(o)
			if started, _ := e.egress.Snapshot(); len(started) != 0 {
				t.Fatalf("no egress may start: %+v", started)
			}
			if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE session_id = $1`, id) != 0 {
				t.Fatal("no recording row may exist")
			}
			if reasons := e.skippedReasons(id); !slices.Contains(reasons, tc.reason) {
				t.Fatalf("skip must be audited with %q, got %v", tc.reason, reasons)
			}
		})
	}
}

var keyPattern = regexp.MustCompile(`^ten_[A-Za-z0-9]{22}/ses_[A-Za-z0-9]{22}/rec_[A-Za-z0-9]{22}\.ogg$`)

func TestRecordingFollowsOpenQuestionsAndFeedsAudioRefs(t *testing.T) {
	e := newEnv(t, allowed)
	id, room, lease := e.toFirstQuestion(allowed)
	started, _ := e.egress.Snapshot()
	if len(started) != 1 || started[0].Room != room || started[0].TrackSID != "TR_mic0001" || !keyPattern.MatchString(started[0].Filepath) {
		t.Fatalf("one egress of the candidate microphone with an opaque key: %+v", started)
	}
	if !strings.Contains(started[0].Filepath, id) {
		t.Fatal("the key is scoped to the session")
	}
	if e.svc.RecordingActive(ctx, id) != true {
		t.Fatal("a starting recording is reported as active")
	}
	tier1Pool, err := store.Open(ctx, e.db.URLFor("ati_tier1_ctx"), 2)
	e.must(err)
	t.Cleanup(tier1Pool.Close)
	tier1 := tier1ctx.New(tier1Pool, nil)
	c, err := tier1.Build(ctx, id)
	e.must(err)
	if !c.RecordingActive {
		t.Fatal("Tier-1 context must disclose that recording is active")
	}

	// The egress service reports the recording started; candidate speech gets an audio reference.
	e.must(e.rec.OnEgressEvent(ctx, &realtime.Webhook{Event: "egress_started", EgressID: started[0].EgressID, EgressStatus: "EGRESS_ACTIVE"}))
	var startedMs int64
	e.must(e.db.CP.QueryRow(ctx, `SELECT started_ms FROM ati.audio_recordings WHERE session_id = $1 AND status = 'active'`, id).Scan(&startedMs))
	act, err := e.svc.CandidateView(ctx, id, "test")
	e.must(err)
	e.must(e.svc.PromptStarted(ctx, agent, lease, id, act.ActivePrompt.ActivationID, startedMs+100))
	res, err := e.svc.IngestSegment(ctx, agent, lease, id, interview.Submission{SegmentKey: "rec-seg-00001", Speaker: "candidate", Text: "answer",
		StartMs: startedMs + 500, EndMs: startedMs + 900, Source: "synthetic_test", Engine: "scripted"})
	e.must(err)
	var recID string
	var offset int64
	e.must(e.db.CP.QueryRow(ctx, `SELECT audio_recording_id, audio_offset_ms FROM ati.transcript_segments WHERE id = $1`, res.SegmentID).Scan(&recID, &offset))
	if offset != 500 || !strings.HasPrefix(recID, "rec_") {
		t.Fatalf("audio_ref = (%s, %d), want offset 500", recID, offset)
	}

	// Pause stops the recording; resume starts a new one.
	v := e.version(id)
	e.must(e.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
	_, stopped := e.egress.Snapshot()
	if len(stopped) != 1 || stopped[0] != started[0].EgressID {
		t.Fatalf("pause must stop the egress: %v", stopped)
	}
	if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE session_id = $1 AND status = 'complete' AND ended_ms >= started_ms`, id) != 1 {
		t.Fatal("the stopped recording is complete with an end bound")
	}
	if e.svc.RecordingActive(ctx, id) {
		t.Fatal("no recording while paused")
	}
	e.must(e.svc.Resume(ctx, candidate, id, e.version(id)))
	if started, _ = e.egress.Snapshot(); len(started) != 2 || started[1].Filepath == started[0].Filepath {
		t.Fatalf("resume starts a new recording segment: %+v", started)
	}

	// Withdrawal stops it; finished recordings are never deleted by the application.
	e.must(e.svc.Withdraw(ctx, candidate, id, e.version(id)))
	if _, stopped = e.egress.Snapshot(); len(stopped) != 2 {
		t.Fatalf("withdrawal must stop the egress: %v", stopped)
	}
	if _, err := e.db.CP.Exec(ctx, `DELETE FROM ati.audio_recordings WHERE session_id = $1`, id); err == nil {
		t.Fatal("the control-plane role must not delete recording rows")
	}
}

// Completing the interview (T11) stops the recording: the egress ends and the row is complete.
func TestCompletionStopsTheRecording(t *testing.T) {
	e := newEnv(t, allowed)
	id, _, lease := e.toFirstQuestion(allowed)
	started, _ := e.egress.Snapshot()
	if len(started) != 1 {
		t.Fatalf("one recording expected: %+v", started)
	}
	e.must(e.rec.OnEgressEvent(ctx, &realtime.Webhook{Event: "egress_started", EgressID: started[0].EgressID, EgressStatus: "EGRESS_ACTIVE"}))
	var epoch time.Time
	e.must(e.db.CP.QueryRow(ctx, `SELECT timeline_epoch FROM ati.interview_sessions WHERE id = $1`, id).Scan(&epoch))
	ms := func() int64 { return time.Since(epoch).Milliseconds() }
	for i := 0; ; i++ {
		v, err := e.svc.CandidateView(ctx, id, "test")
		e.must(err)
		if v.State == flow.InterviewCompleted {
			break
		}
		if i > 8 {
			t.Fatalf("the interview did not complete: %s", v.State)
		}
		if _, stopped := e.egress.Snapshot(); len(stopped) != 0 {
			t.Fatalf("the recording stopped before the interview completed (item %d)", i)
		}
		act := v.ActivePrompt.ActivationID
		e.must(e.svc.PromptStarted(ctx, agent, lease, id, act, ms()))
		time.Sleep(15 * time.Millisecond)
		start := ms()
		time.Sleep(15 * time.Millisecond)
		key := fmt.Sprintf("t11-seg-%05d", i)
		_, err = e.svc.IngestSegment(ctx, agent, lease, id, interview.Submission{SegmentKey: key, Speaker: "candidate", Text: "answer",
			StartMs: start, EndMs: ms(), Source: "synthetic_test", Engine: "scripted"})
		e.must(err)
		e.must(e.svc.AgentAnswerComplete(ctx, agent, lease, id, e.version(id), act, []string{key}, "agent_tool"))
	}
	if _, stopped := e.egress.Snapshot(); len(stopped) != 1 || stopped[0] != started[0].EgressID {
		t.Fatalf("completion must stop the egress: %v", stopped)
	}
	if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE session_id = $1 AND status = 'complete' AND ended_ms >= started_ms`, id) != 1 {
		t.Fatal("the recording is complete with an end bound")
	}
	if e.svc.RecordingActive(ctx, id) {
		t.Fatal("no recording after the interview")
	}
}

// A microphone published before the first question (calibration) is not recorded: calibration
// input is never evidence and the policy records only answers.
func TestNoRecordingOutsideAnOpenQuestion(t *testing.T) {
	e := newEnv(t, allowed)
	c, err := e.svc.CreateInterview(ctx, staff, interview.CreateRequest{TenantID: e.tenant, StaffID: "stf_test", CandidateRef: ids.New("ref"), PlanID: "plan_demo_backend_en", PlanVersion: 1})
	e.must(err)
	_, err = e.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	e.must(err)
	e.must(e.svc.SubmitConsent(ctx, candidate, c.SessionID, "notice_default", "2026-11-draft.1", map[string]bool{
		"voice_interview_processing": true, "transcript_assessment_record": true, "ai_assisted_evaluation_with_human_review": true, "audio_recording": true,
	}))
	e.must(e.svc.Start(ctx, candidate, c.SessionID, e.version(c.SessionID), "voice"))
	room, _, _, err := e.svc.RoomFor(ctx, c.SessionID)
	e.must(err)
	e.media.Join(room, realtime.CandidateIdentity, false)
	e.media.PublishMic(room, realtime.CandidateIdentity, "TR_mic0003")
	e.rec.Start(ctx, c.SessionID) // the track_published webhook during calibration
	if started, _ := e.egress.Snapshot(); len(started) != 0 {
		t.Fatalf("nothing may be recorded during calibration: %+v", started)
	}
	if reasons := e.skippedReasons(c.SessionID); !slices.Contains(reasons, recording.ReasonNotAnswering) {
		t.Fatalf("skip must be audited with %q, got %v", recording.ReasonNotAnswering, reasons)
	}
}

func TestMicrophoneTrackEventsStartAndStopRecording(t *testing.T) {
	o := allowed
	o.noMic = true
	e := newEnv(t, o)
	id, room, _ := e.toFirstQuestion(o)
	if started, _ := e.egress.Snapshot(); len(started) != 0 {
		t.Fatal("no track yet: nothing to record")
	}
	e.media.PublishMic(room, realtime.CandidateIdentity, "TR_mic0002")
	e.rec.Start(ctx, id) // the track_published webhook
	e.rec.Start(ctx, id) // duplicate delivery: idempotent
	started, _ := e.egress.Snapshot()
	if len(started) != 1 || started[0].TrackSID != "TR_mic0002" {
		t.Fatalf("publishing the microphone starts one recording: %+v", started)
	}
	e.rec.Stop(ctx, id) // the track_unpublished webhook
	if _, stopped := e.egress.Snapshot(); len(stopped) != 1 {
		t.Fatal("unpublishing the microphone stops the recording")
	}
}

func TestEgressFailuresFailClosedAndAreAudited(t *testing.T) {
	e := newEnv(t, allowed)
	e.egress.Fail = errors.New("egress unavailable")
	id, _, _ := e.toFirstQuestion(allowed)
	if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE session_id = $1 AND status = 'failed'`, id) != 1 ||
		e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'recording.failed'`, id) != 1 {
		t.Fatal("a failed start is recorded and audited")
	}
	if e.svc.RecordingActive(ctx, id) {
		t.Fatal("a failed recording is not active")
	}
	// A later failure reported by the egress service.
	e.egress.Fail = nil
	v := e.version(id)
	e.must(e.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
	e.must(e.svc.Resume(ctx, candidate, id, e.version(id)))
	started, _ := e.egress.Snapshot()
	e.must(e.rec.OnEgressEvent(ctx, &realtime.Webhook{Event: "egress_ended", EgressID: started[0].EgressID, EgressStatus: "EGRESS_FAILED"}))
	if e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'recording.failed'`, id) != 2 {
		t.Fatal("an egress failure is audited")
	}
	// Events for unknown egresses are ignored.
	e.must(e.rec.OnEgressEvent(ctx, &realtime.Webhook{Event: "egress_ended", EgressID: "EG_unknown", EgressStatus: "EGRESS_COMPLETE"}))
}

// LiveKit never answers a start request when no egress service is connected (observed against
// LiveKit OSS in the E2E run): the recording fails within the egress timeout instead of staying
// "starting", and a caller that gives up early cannot leave it half-started either.
func TestAnUnansweredEgressStartFailsClosedWithinTheTimeout(t *testing.T) {
	o := allowed
	o.egressTimeout = 150 * time.Millisecond
	e := newEnv(t, o)
	e.egress.Hang = true
	began := time.Now()
	id, _, _ := e.toFirstQuestion(o)
	if d := time.Since(began); d > 5*time.Second {
		t.Fatalf("the start was not bounded: %v", d)
	}
	var status, failure string
	e.must(e.db.CP.QueryRow(ctx, `SELECT status, failure FROM ati.audio_recordings WHERE session_id = $1`, id).Scan(&status, &failure))
	if status != "failed" || !strings.Contains(failure, "timed out") {
		t.Fatalf("an unanswered start must fail: %s %q", status, failure)
	}
	if e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'recording.failed'`, id) != 1 || e.svc.RecordingActive(ctx, id) {
		t.Fatal("the failure is audited and nothing is reported as recording")
	}
	e.egress.Hang = false
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	e.rec.Start(cancelled, id) // e.g. the webhook request ended before the start finished
	if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE session_id = $1 AND status = 'starting' AND egress_id IS NOT NULL`, id) != 1 {
		t.Fatal("a start must complete even when its caller was cancelled")
	}
}

func TestPurgeDeletesExpiredRecordingsAndLeavesTombstones(t *testing.T) {
	backend := s3mem.New()
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	endpoint := strings.TrimPrefix(srv.URL, "http://")
	const bucket = "recordings"
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4("test-only-access", "test-only-secret", "")})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	store, err := recording.NewS3Store(endpoint, bucket, "test-only-access", "test-only-secret", false)
	if err != nil {
		t.Fatal(err)
	}

	e := newEnv(t, allowed)
	id, _, _ := e.toFirstQuestion(allowed)
	v := e.version(id)
	e.must(e.svc.Pause(ctx, candidate, nil, id, &v, "candidate_request"))
	e.must(e.svc.Resume(ctx, candidate, id, e.version(id))) // second recording stays live
	type rec struct{ id, key, status string }
	var recs []rec
	rows, err := e.db.CP.Query(ctx, `SELECT id, object_key, status FROM ati.audio_recordings WHERE session_id = $1 ORDER BY created_at`, id)
	e.must(err)
	for rows.Next() {
		var r rec
		e.must(rows.Scan(&r.id, &r.key, &r.status))
		recs = append(recs, r)
	}
	rows.Close()
	if len(recs) != 2 || recs[0].status != "complete" || recs[1].status != "starting" {
		t.Fatalf("one finished and one live recording expected: %+v", recs)
	}
	for _, r := range recs { // what the egress service would have written
		if _, err := client.PutObject(ctx, bucket, r.key, bytes.NewReader([]byte("OggS synthetic")), 14, minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing has expired yet.
	if n, err := recording.PurgeExpired(ctx, e.db.CP, store, time.Now()); err != nil || n != 0 {
		t.Fatalf("purge before expiry: n=%d err=%v", n, err)
	}
	// After the retention period only the finished recording is purged.
	later := time.Now().AddDate(0, 0, 31)
	if n, err := recording.PurgeExpired(ctx, e.db.CP, store, later); err != nil || n != 1 {
		t.Fatalf("purge after expiry: n=%d err=%v", n, err)
	}
	if _, err := client.StatObject(ctx, bucket, recs[0].key, minio.StatObjectOptions{}); err == nil {
		t.Fatal("the expired object must be deleted")
	}
	if _, err := client.StatObject(ctx, bucket, recs[1].key, minio.StatObjectOptions{}); err != nil {
		t.Fatal("a live recording must never be purged")
	}
	if e.count(`SELECT count(*) FROM ati.audio_recordings WHERE id = $1 AND purged_at IS NOT NULL AND purge_reason = 'retention_expired'`, recs[0].id) != 1 ||
		e.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'recording.purged' AND details->>'recording_id' = $1`, recs[0].id) != 1 {
		t.Fatal("the purge leaves an audited tombstone")
	}
	// Idempotent.
	if n, err := recording.PurgeExpired(ctx, e.db.CP, store, later); err != nil || n != 0 {
		t.Fatalf("second purge: n=%d err=%v", n, err)
	}
}

// A restart can interrupt a start after LiveKit began the egress but before the control plane
// stored its id (or before the egress lifecycle webhook arrived), and a stop can fail after the
// row says "stopping". Reconciliation stops whatever still writes a recording's file and leaves no
// recording "starting" or "stopping" past their bounds.
func TestReconcileSettlesInterruptedStartsAndStops(t *testing.T) {
	e := newEnv(t, allowed)
	recordingOf := func(id string) (recID, egressID, key string) {
		var eg *string
		e.must(e.db.CP.QueryRow(ctx, `SELECT id, egress_id, object_key FROM ati.audio_recordings WHERE session_id = $1`, id).Scan(&recID, &eg, &key))
		if eg != nil {
			egressID = *eg
		}
		return
	}
	age := func(id, set string) {
		_, err := e.db.Owner.Exec(ctx, `UPDATE ati.audio_recordings SET `+set+`, updated_at = now() - interval '1 minute' WHERE session_id = $1`, id)
		e.must(err)
	}
	status := func(id string) (string, string) {
		var s string
		var f *string
		e.must(e.db.CP.QueryRow(ctx, `SELECT status, failure FROM ati.audio_recordings WHERE session_id = $1`, id).Scan(&s, &f))
		if f == nil {
			return s, ""
		}
		return s, *f
	}
	// 1. The start returned but its lifecycle webhook never came.
	lost, _, _ := e.toFirstQuestion(allowed)
	_, lostEgress, _ := recordingOf(lost)
	age(lost, "status = 'starting'")
	// 2. LiveKit started the egress but its id was never stored.
	untracked, _, _ := e.toFirstQuestion(allowed)
	_, untrackedEgress, _ := recordingOf(untracked)
	age(untracked, "egress_id = NULL")
	// 3. A stop whose egress call failed.
	stopping, _, _ := e.toFirstQuestion(allowed)
	_, stoppingEgress, _ := recordingOf(stopping)
	age(stopping, "status = 'stopping'")
	// 4. A start still within its bound is left alone.
	recent, _, _ := e.toFirstQuestion(allowed)

	n, err := e.rec.Reconcile(ctx)
	e.must(err)
	if n != 3 {
		t.Fatalf("settled %d recordings, want 3", n)
	}
	_, stopped := e.egress.Snapshot()
	for _, eg := range []string{lostEgress, untrackedEgress, stoppingEgress} {
		if !slices.Contains(stopped, eg) {
			t.Fatalf("egress %s still running: stopped=%v", eg, stopped)
		}
	}
	for _, id := range []string{lost, untracked} {
		if s, f := status(id); s != "failed" || f != "start interrupted; egress stopped" {
			t.Fatalf("an interrupted start fails: %s %q", s, f)
		}
		if e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'recording.failed'`, id) != 1 {
			t.Fatal("the failure is audited")
		}
		if e.svc.RecordingActive(ctx, id) {
			t.Fatal("a failed recording is not active")
		}
	}
	if _, eg, _ := recordingOf(untracked); eg != untrackedEgress {
		t.Fatalf("the untracked egress is recorded on the failed recording: %q", eg)
	}
	if s, _ := status(stopping); s != "complete" {
		t.Fatalf("a retried stop completes: %s", s)
	}
	if s, _ := status(recent); s != "starting" {
		t.Fatalf("a start within its bound is untouched: %s", s)
	}
	// The media server cannot list egresses: the recording still fails, visibly.
	unknown, _, _ := e.toFirstQuestion(allowed)
	age(unknown, "status = 'starting'")
	e.egress.FailList = errors.New("egress service unavailable")
	_, err = e.rec.Reconcile(ctx)
	e.must(err)
	if s, f := status(unknown); s != "failed" || f != "start interrupted; egress state unknown" {
		t.Fatalf("unknown egress state: %s %q", s, f)
	}
}
