package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/logging"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

const (
	origin       = "http://localhost:3000"
	agentToken   = "ati-dev-agent-service-token-for-tests-0001"
	staffRowID = "stf_testtoken000000000000"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t        *testing.T
	db       *testdb.DB
	media    *realtime.Fake
	public   *httptest.Server
	internal *httptest.Server
	staff    string
	tenant   string
	logs     *syncBuffer
	keys     atomic.Int64
	svc      *interview.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testdb.New(t)
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"APP_ENV": "test", "ATI_PUBLIC_ORIGIN": origin, "ATI_DATABASE_URL": "unused", "ATI_TIER1_CTX_DATABASE_URL": "unused",
			"ATI_LIVEKIT_URL": "ws://localhost:7880", "ATI_LIVEKIT_API_KEY": "k", "ATI_LIVEKIT_API_SECRET": "ati-dev-secret",
			"ATI_LIVEKIT_WEBHOOK_API_KEY": "wk", "ATI_LIVEKIT_WEBHOOK_API_SECRET": "ati-dev-wsecret",
			"ATI_AGENT_SERVICE_TOKEN_SHA256": ids.Hash(agentToken), "ATI_RETENTION_TRANSCRIPT_DAYS": "30",
			"ATI_RETENTION_SESSION_EVENTS_DAYS": "30", "ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS": "30", "ATI_INVITATION_EXCHANGES_PER_MINUTE": "5",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	log := logging.New(logs, "debug", "control-plane-test")
	m, _ := flow.Default()
	media := realtime.NewFake()
	svc, err := interview.New(db.CP, m, interview.Settings{
		CompletionDeadline: 8 * time.Second, PauseTimeout: 30 * time.Minute, InvitationTTL: time.Hour, CandidateSessionTTL: time.Hour,
		LeaseTTL: 15 * time.Second, Retention: cfg.Retention, NoticeID: cfg.NoticeID, NoticeVersion: cfg.NoticeVersion,
	}, api.Effects{Media: media, Log: log}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	tier1Pool, err := store.Open(context.Background(), db.URLFor("ati_tier1_ctx"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tier1Pool.Close)
	srv := api.New(api.Deps{Config: cfg, Service: svc, Tier1: tier1ctx.New(tier1Pool, nil), Media: media, Pool: db.CP, Tier1Pool: tier1Pool, Log: log})
	e := &env{t: t, db: db, media: media, logs: logs, svc: svc, tenant: ids.New(ids.Tenant)}
	e.public = httptest.NewServer(srv.PublicHandler())
	e.internal = httptest.NewServer(srv.InternalHandler())
	t.Cleanup(e.public.Close)
	t.Cleanup(e.internal.Close)
	e.staff = api.StaffTokenPrefix + ids.Token()
	if _, err := db.Owner.Exec(context.Background(), `INSERT INTO ati.tenants (id, name) VALUES ($1,'T')`, e.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,'recruiter','test',now() + interval '1 day')`,
		staffRowID, e.tenant, ids.Hash(e.staff)); err != nil {
		t.Fatal(err)
	}
	return e
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    []byte
}

func (r response) str(path ...string) string {
	var cur any = r.body
	for _, p := range path {
		m, _ := cur.(map[string]any)
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

func (r response) num(path ...string) int64 {
	var cur any = r.body
	for _, p := range path {
		m, _ := cur.(map[string]any)
		cur = m[p]
	}
	f, _ := cur.(float64)
	return int64(f)
}

type req struct {
	method, url string
	body        any
	headers     map[string]string
	cookie      string
}

func (e *env) do(q req) response {
	e.t.Helper()
	var rd io.Reader
	if q.body != nil {
		b, _ := json.Marshal(q.body)
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequest(q.method, q.url, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if q.body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if q.method == http.MethodPost {
		r.Header.Set("Idempotency-Key", fmt.Sprintf("idem-%010d", e.keys.Add(1)))
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	if q.cookie != "" {
		r.Header.Set("Cookie", api.CandidateCookie+"="+q.cookie)
		if _, ok := q.headers["Origin"]; !ok {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, header: resp.Header, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// candidateCookie extracts the session cookie from an exchange response.
func candidateCookie(t *testing.T, r response) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.CandidateCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api" {
				t.Fatalf("cookie attributes: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatalf("no session cookie in %v", r.header)
	return ""
}

// interviewToReady creates an interview over the API and drives it to READY.
func (e *env) interviewToReady() (id, cookie string) {
	e.t.Helper()
	r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews", headers: map[string]string{"Authorization": "Bearer " + e.staff},
		body: map[string]any{"candidate_ref": ids.New("ref"), "plan_id": "plan_demo_backend_en", "plan_version": 1}})
	if r.status != 201 {
		e.t.Fatalf("create: %d %s", r.status, r.raw)
	}
	url := r.str("invitation_url")
	if !strings.HasPrefix(url, origin+"/i#token=") {
		e.t.Fatalf("invitation must carry the token in the fragment: %s", url)
	}
	token := strings.TrimPrefix(url, origin+"/i#token=")
	ex := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", body: map[string]any{"token": token}, headers: map[string]string{"Origin": origin}})
	if ex.status != 200 {
		e.t.Fatalf("exchange: %d %s", ex.status, ex.raw)
	}
	id, cookie = ex.str("interview_id"), candidateCookie(e.t, ex)
	c := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/consent", cookie: cookie, body: map[string]any{
		"notice_id": "notice_default", "notice_version": "2026-11-draft.1",
		"decisions": []map[string]any{{"purpose_id": "voice_interview_processing", "granted": true}, {"purpose_id": "transcript_assessment_record", "granted": true},
			{"purpose_id": "ai_assisted_evaluation_with_human_review", "granted": true}, {"purpose_id": "audio_recording", "granted": false}}}})
	if c.status != 200 || c.str("state") != "READY" {
		e.t.Fatalf("consent: %d %s", c.status, c.raw)
	}
	return id, cookie
}

type agentSession struct {
	id, grant string
	fencing   int64
}

func (a agentSession) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + agentToken, "X-ATI-Agent-Grant": a.grant, "X-ATI-Fencing-Token": fmt.Sprint(a.fencing)}
}

// startAndClaim starts the interview, requests media, and claims the dispatch as the agent.
func (e *env) startAndClaim(id, cookie string) (agentSession, response) {
	e.t.Helper()
	st := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/start", cookie: cookie, body: map[string]any{"state_version": 3, "input_mode": "voice"}})
	if st.status != 200 || st.str("state") != "CALIBRATION" {
		e.t.Fatalf("start: %d %s", st.status, st.raw)
	}
	rt := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie})
	if rt.status != 200 || rt.str("identity") != "candidate" || rt.body["can_publish_audio"] != true {
		e.t.Fatalf("realtime-session: %d %s", rt.status, rt.raw)
	}
	room := rt.str("room")
	ds, _ := e.media.ListDispatches(context.Background(), room)
	if len(ds) != 1 {
		e.t.Fatalf("expected exactly one dispatch, got %d", len(ds))
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(ds[0].Metadata), &meta)
	if len(meta) != 2 || meta["session_id"] != id || meta["claim_nonce"] == nil {
		e.t.Fatalf("dispatch metadata must hold only session_id and claim_nonce: %v", meta)
	}
	e.media.Join(room, "candidate", false)
	e.media.Join(room, "agent-AJ_1", true)
	claim := e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/claims", headers: map[string]string{"Authorization": "Bearer " + agentToken},
		body: map[string]any{"session_id": id, "claim_nonce": meta["claim_nonce"], "job_id": "AJ_1", "agent_identity": "agent-AJ_1"}})
	if claim.status != 201 {
		e.t.Fatalf("claim: %d %s", claim.status, claim.raw)
	}
	return agentSession{id: id, grant: claim.str("grant"), fencing: claim.num("fencing_token")}, claim
}
