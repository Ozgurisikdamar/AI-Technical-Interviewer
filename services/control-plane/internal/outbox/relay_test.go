package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/outbox"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
)

var ctx = context.Background()

type fakePublisher struct {
	mu        sync.Mutex
	published []map[string]any
	failAt    int // 1-based publish call that fails (0: none)
	calls     int
}

func (f *fakePublisher) Publish(_ context.Context, stream string, fields map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if stream != outbox.Stream {
		return fmt.Errorf("unexpected stream %s", stream)
	}
	if f.calls == f.failAt {
		return errors.New("redis unavailable")
	}
	f.published = append(f.published, fields)
	return nil
}

type fakeEnqueuer struct{ sessions []string }

func (f *fakeEnqueuer) EnqueueEvaluation(_ context.Context, id string) error {
	f.sessions = append(f.sessions, id)
	return nil
}

// seedOutbox writes a session and its events (identifiers only) as the transition service would.
func seedOutbox(t *testing.T, db *testdb.DB, types ...string) (string, []string) {
	t.Helper()
	tenant, cand, session := ids.New(ids.Tenant), ids.New(ids.Candidate), ids.New(ids.Session)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO ati.tenants (id, name) VALUES ($1, 'T')`, []any{tenant}},
		{`INSERT INTO ati.candidates (id, tenant_id, external_ref) VALUES ($1, $2, 'ref')`, []any{cand, tenant}},
		{`INSERT INTO ati.interview_sessions (id, tenant_id, candidate_id, plan_id, plan_version, notice_id, notice_version, notice_language, state, input_mode,
			timeline_epoch, room_name, retention, recording_policy, created_by)
			VALUES ($1,$2,$3,'plan_demo_backend_en',1,'notice_default','2026-11-draft.1','en','INTERVIEW_COMPLETED','text',now(),$4,'{}','{}','test')`, []any{session, tenant, cand, ids.New(ids.Room)}},
	} {
		if _, err := db.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	var eventIDs []string
	for _, typ := range types {
		id := ids.New(ids.Event)
		if _, err := db.Owner.Exec(ctx, `INSERT INTO ati.outbox_events (id, tenant_id, session_id, event_type, event_version, payload) VALUES ($1,$2,$3,$4,1,$5)`,
			id, tenant, session, typ, fmt.Sprintf(`{"event_id": %q, "session_id": %q}`, id, session)); err != nil {
			t.Fatal(err)
		}
		eventIDs = append(eventIDs, id)
	}
	return session, eventIDs
}

func unpublished(t *testing.T, db *testdb.DB) int {
	t.Helper()
	var n int
	if err := db.Owner.QueryRow(ctx, `SELECT count(*) FROM ati.outbox_events WHERE published_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRelayPublishesInOrderAndEnqueuesBeforeTheCompletionEvent(t *testing.T) {
	db := testdb.New(t)
	session, events := seedOutbox(t, db, "answer.finalized", "answer.finalized", "interview.completed")
	pub, enq := &fakePublisher{}, &fakeEnqueuer{}
	relay := outbox.NewRelay(db.CP, pub, enq, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	n, err := relay.RunOnce(ctx)
	if err != nil || n != 3 {
		t.Fatalf("published %d: %v", n, err)
	}
	var got []string
	for _, f := range pub.published {
		got = append(got, f["event_id"].(string))
		if f["session_id"] != session || f["payload"] == "" {
			t.Fatalf("entry fields: %v", f)
		}
	}
	if !slices.Equal(got, events) {
		t.Fatalf("order %v, want %v", got, events)
	}
	if !slices.Equal(enq.sessions, []string{session}) {
		t.Fatalf("T14 is applied for the completion event only: %v", enq.sessions)
	}
	if n, err := relay.RunOnce(ctx); err != nil || n != 0 || unpublished(t, db) != 0 {
		t.Fatalf("a second pass publishes nothing: %d %v", n, err)
	}
}

func TestRelayStopsAtAFailureAndResumesInOrder(t *testing.T) {
	db := testdb.New(t)
	_, events := seedOutbox(t, db, "answer.finalized", "answer.finalized", "answer.finalized")
	pub := &fakePublisher{failAt: 2}
	relay := outbox.NewRelay(db.CP, pub, &fakeEnqueuer{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if n, err := relay.RunOnce(ctx); err == nil || n != 1 || unpublished(t, db) != 2 {
		t.Fatalf("the pass must stop at the failure: n=%d err=%v unpublished=%d", n, err, unpublished(t, db))
	}
	if n, err := relay.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("resume: %d %v", n, err)
	}
	var got []string
	for _, f := range pub.published {
		got = append(got, f["event_id"].(string))
	}
	if !slices.Equal(got, events) {
		t.Fatalf("order after a failure %v, want %v", got, events)
	}
}

// The Redis publisher against a real local redis-server: entries land on the stream with every field.
func TestRedisPublisherAppendsToTheStream(t *testing.T) {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not installed")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	srv := exec.Command(bin, "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	pub, err := outbox.NewRedisPublisher(fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	deadline := time.Now().Add(5 * time.Second)
	for pub.Client.Ping(ctx).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("redis-server did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := pub.Publish(ctx, outbox.Stream, map[string]any{"event_id": "evt_x", "event_type": "interview.completed", "payload": "{}"}); err != nil {
		t.Fatal(err)
	}
	entries, err := pub.Client.XRange(ctx, outbox.Stream, "-", "+").Result()
	if err != nil || len(entries) != 1 || entries[0].Values["event_id"] != "evt_x" || entries[0].Values["event_type"] != "interview.completed" {
		t.Fatalf("stream: %+v %v", entries, err)
	}
	_ = redis.Nil
}
