package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"regexp"
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
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
)

var ctx = context.Background()

func noEnv(string) string { return "" }

type noEffects struct{}

func (noEffects) DeleteRoom(context.Context, string, string) {}
func (noEffects) StartRecording(context.Context, string)     {}
func (noEffects) StopRecording(context.Context, string)      {}

func service(t *testing.T, db *testdb.DB) *interview.Service {
	t.Helper()
	m, err := flow.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := interview.New(db.CP, m, interview.Settings{
		CompletionDeadline: 8 * time.Second, PauseTimeout: 30 * time.Minute, InvitationTTL: time.Hour, CandidateSessionTTL: time.Hour, LeaseTTL: time.Minute,
		Retention: config.Retention{TranscriptDays: 30, SessionEventsDays: 30, AssessmentOutputDays: 30}, NoticeID: "notice_default", NoticeVersion: "2026-11-draft.1",
	}, noEffects{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func admin(t *testing.T, cmd string, getenv func(string) string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(ctx, cmd, args, &out, getenv); err != nil {
		t.Fatalf("%s %v: %v", cmd, args, err)
	}
	return strings.TrimSpace(out.String())
}

func TestSchemaCommandsAreIdempotent(t *testing.T) {
	db := testdb.New(t)
	owner := db.URLFor("postgres")
	admin(t, "bootstrap-roles", noEnv, "--admin-url", owner)
	admin(t, "migrate", noEnv, "--url", owner)
	admin(t, "seed-plans", noEnv, "--url", owner)
}

func TestTenantAndStaffTokenAreUsableAndStoredOnlyAsHashes(t *testing.T) {
	db := testdb.New(t)
	owner := db.URLFor("postgres")
	tenant := admin(t, "create-tenant", noEnv, "--url", owner, "--name", "Acme")
	if !regexp.MustCompile(`^ten_[A-Za-z0-9]{16,}$`).MatchString(tenant) {
		t.Fatalf("tenant id %q", tenant)
	}
	token := admin(t, "create-staff-token", noEnv, "--url", owner, "--tenant", tenant, "--role", "admin", "--label", "ops", "--ttl", "1h")
	if !strings.HasPrefix(token, "ati_st_") || len(token) < 40 {
		t.Fatalf("token %q", token[:min(10, len(token))])
	}
	p, err := service(t, db).AuthenticateStaff(ctx, ids.Hash(token))
	if err != nil || p.TenantID != tenant || p.Role != "admin" {
		t.Fatalf("the printed token must authenticate as an admin of the tenant: %+v %v", p, err)
	}
	var expires time.Time
	var leaks int
	if err := db.Owner.QueryRow(ctx, `SELECT expires_at, (SELECT count(*) FROM ati.staff_tokens s WHERE s::text LIKE '%' || $2 || '%')
		FROM ati.staff_tokens WHERE token_hash = $1`, ids.Hash(token), token).Scan(&expires, &leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatal("the token must be stored only as a hash")
	}
	if d := time.Until(expires); d < 55*time.Minute || d > 65*time.Minute {
		t.Fatalf("expiry in %v, want about 1h", d)
	}
	for name, args := range map[string][]string{
		"unknown role": {"--url", owner, "--tenant", tenant, "--role", "viewer", "--label", "x"},
		"no label":     {"--url", owner, "--tenant", tenant},
		"negative ttl": {"--url", owner, "--tenant", tenant, "--label", "x", "--ttl", "-1h"},
		"bad flag":     {"--url", owner, "--nope"},
	} {
		if err := run(ctx, "create-staff-token", args, io.Discard, noEnv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := run(ctx, "create-tenant", []string{"--url", owner}, io.Discard, noEnv); err == nil {
		t.Error("a tenant without a name was created")
	}
	if err := run(ctx, "drop-everything", nil, io.Discard, noEnv); !errors.Is(err, errUnknownCommand) {
		t.Errorf("unknown command: %v", err)
	}
}

func TestTenantRecordingPolicyIsAudited(t *testing.T) {
	db := testdb.New(t)
	owner := db.URLFor("postgres")
	tenant := admin(t, "create-tenant", noEnv, "--url", owner, "--name", "Acme")
	admin(t, "tenant-recording", noEnv, "--url", owner, "--tenant", tenant, "--allowed=true")
	var allowed bool
	var audits int
	if err := db.Owner.QueryRow(ctx, `SELECT recording_allowed, (SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'tenant.recording_policy'
		AND details->>'recording_allowed' = 'true') FROM ati.tenants WHERE id = $1`, tenant).Scan(&allowed, &audits); err != nil {
		t.Fatal(err)
	}
	if !allowed || audits != 1 {
		t.Fatalf("allowed=%v audits=%d", allowed, audits)
	}
	admin(t, "tenant-recording", noEnv, "--url", owner, "--tenant", tenant, "--allowed=false")
	if err := db.Owner.QueryRow(ctx, `SELECT recording_allowed FROM ati.tenants WHERE id = $1`, tenant).Scan(&allowed); err != nil || allowed {
		t.Fatalf("the policy must be revocable: %v %v", allowed, err)
	}
	if err := run(ctx, "tenant-recording", []string{"--url", owner, "--tenant", "ten_doesnotexist0000000", "--allowed=true"}, io.Discard, noEnv); err == nil {
		t.Fatal("an unknown tenant was accepted")
	}
}

func TestPurgeExpiredUsesTheConfiguredStorage(t *testing.T) {
	backend := s3mem.New()
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	endpoint, bucket := strings.TrimPrefix(srv.URL, "http://"), "recordings"
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4("test-only-access", "test-only-secret", "")})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	db := testdb.New(t)
	tenant := ids.New(ids.Tenant)
	if _, err := db.Owner.Exec(ctx, `INSERT INTO ati.tenants (id, name, recording_allowed) VALUES ($1, 'T', true)`, tenant); err != nil {
		t.Fatal(err)
	}
	created, err := service(t, db).CreateInterview(ctx, interview.Actor{Type: flow.ActorSystem, ID: "stf_admin_test"}, interview.CreateRequest{
		TenantID: tenant, StaffID: "stf_admin_test", CandidateRef: ids.New("ref"), PlanID: "plan_demo_backend_en", PlanVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	expired, live := ids.New("rec"), ids.New("rec")
	for _, r := range []struct {
		id, status string
		until      time.Duration
	}{{expired, "complete", -time.Hour}, {live, "complete", 24 * time.Hour}} {
		key := tenant + "/" + created.SessionID + "/" + r.id + ".ogg"
		if _, err := db.Owner.Exec(ctx, `INSERT INTO ati.audio_recordings (id, tenant_id, session_id, track_sid, object_key, status, started_ms, ended_ms, retention_until)
			VALUES ($1,$2,$3,'TR_x',$4,$5,0,10,$6)`, r.id, tenant, created.SessionID, key, r.status, time.Now().Add(r.until)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.PutObject(ctx, bucket, key, bytes.NewReader([]byte("OggS synthetic")), 14, minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	getenv := func(k string) string {
		return map[string]string{"ATI_RECORDING_S3_ENDPOINT": endpoint, "ATI_RECORDING_S3_BUCKET": bucket,
			"ATI_RECORDING_S3_ACCESS_KEY": "test-only-access", "ATI_RECORDING_S3_SECRET_KEY": "test-only-secret"}[k]
	}
	cp := db.URLFor("ati_control_plane")
	if out := admin(t, "recordings", getenv, "--url", cp, "purge-expired"); out != "purged 1 recording(s)" {
		t.Fatalf("output %q", out)
	}
	if _, err := client.StatObject(ctx, bucket, tenant+"/"+created.SessionID+"/"+expired+".ogg", minio.StatObjectOptions{}); err == nil {
		t.Fatal("the expired object must be deleted")
	}
	if _, err := client.StatObject(ctx, bucket, tenant+"/"+created.SessionID+"/"+live+".ogg", minio.StatObjectOptions{}); err != nil {
		t.Fatal("an unexpired recording must be kept")
	}
	var tombstones int
	if err := db.Owner.QueryRow(ctx, `SELECT count(*) FROM ati.audio_recordings WHERE id = $1 AND purge_reason = 'retention_expired'`, expired).Scan(&tombstones); err != nil || tombstones != 1 {
		t.Fatalf("tombstone: %d %v", tombstones, err)
	}
	if out := admin(t, "recordings", getenv, "--url", cp, "purge-expired"); out != "purged 0 recording(s)" {
		t.Fatalf("second run %q", out)
	}
	if err := run(ctx, "recordings", []string{"--url", cp}, io.Discard, getenv); err == nil {
		t.Fatal("recordings without a subcommand was accepted")
	}
}

// Webhook dedupe rows and idempotency records (whose replays hold response bodies) are purged once
// they are older than the window; the purge is audited and keeps recent rows.
func TestPurgeOperationalRecordsIsBoundedAndAudited(t *testing.T) {
	db := testdb.New(t)
	owner := db.URLFor("postgres")
	for _, q := range []string{
		`INSERT INTO ati.webhook_events (event_id, event_type, created_unix, received_at) VALUES ('EV_old', 'participant_left', 1, now() - interval '3 days'), ('EV_new', 'participant_left', 1, now())`,
		`INSERT INTO ati.idempotency_records (principal, idem_key, route, request_sha256, status, response, created_at) VALUES
			('p', 'old-key-0001', 'POST /x', 'h', 201, '{"body": {"candidate_ref": "ref"}}', now() - interval '3 days'),
			('p', 'new-key-0001', 'POST /x', 'h', 201, '{}', now())`,
	} {
		if _, err := db.Owner.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(ctx, "purge-operational", []string{"--url", owner, "--older-than", "10m"}, io.Discard, noEnv); err == nil {
		t.Fatal("a window shorter than an hour must be refused")
	}
	out := admin(t, "purge-operational", noEnv, "--url", owner)
	if out != "purged 1 webhook event(s) and 1 idempotency record(s)" {
		t.Fatalf("output: %q", out)
	}
	var webhooks, replays, audits int
	if err := db.Owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM ati.webhook_events), (SELECT count(*) FROM ati.idempotency_records),
		(SELECT count(*) FROM ati.audit_logs WHERE action = 'retention.purge_operational' AND details->>'webhook_events' = '1')`).Scan(&webhooks, &replays, &audits); err != nil {
		t.Fatal(err)
	}
	if webhooks != 1 || replays != 1 || audits != 1 {
		t.Fatalf("webhooks=%d replays=%d audits=%d", webhooks, replays, audits)
	}
}
