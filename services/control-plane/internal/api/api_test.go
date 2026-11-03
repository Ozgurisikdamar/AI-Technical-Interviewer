package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
)

func TestHTTPFlowFromInvitationToCompletion(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	agent, claim := e.startAndClaim(id, cookie)
	ctxRaw, _ := json.Marshal(claim.body["context"])
	if err := schema.ValidateJSON(contracts.Tier1ContextSchemaPath, ctxRaw); err != nil {
		t.Fatalf("claim context violates the Tier-1 contract: %v", err)
	}
	base := e.internal.URL + "/internal/v1/tier1/sessions/" + id

	// Calibration: the agent hears the candidate, then begins.
	seg := func(key string, startMs, endMs int64, text string) response {
		return e.do(req{method: "POST", url: base + "/transcript-segments", headers: agent.headers(), body: map[string]any{
			"segment_key": key, "speaker": "candidate", "text": text, "start_ms": startMs, "end_ms": endMs, "source": "synthetic_test",
			"confidence": nil, "language": "en", "interrupted": false, "provider_item_id": nil, "asr_model": nil, "engine": "scripted",
			"signals": map[string]any{"overlapped_interviewer_audio": false, "low_energy": false}}})
	}
	time.Sleep(30 * time.Millisecond)
	if r := seg("calib-0000001", 0, 10, "hello"); r.status != 201 {
		t.Fatalf("calibration segment: %d %s", r.status, r.raw)
	}
	ctx := e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
	r := e.do(req{method: "POST", url: base + "/calibration-complete", headers: agent.headers(), body: map[string]any{"state_version": ctx.num("state_version")}})
	if r.status != 200 || r.str("state") != "QUESTION_ACTIVE" {
		t.Fatalf("calibration-complete: %d %s", r.status, r.raw)
	}
	for i := 0; ; i++ {
		ctx = e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
		if ctx.str("state") == "INTERVIEW_COMPLETED" {
			break
		}
		if i > 5 {
			t.Fatal("interview did not complete")
		}
		act := ctx.str("active_prompt", "activation_id")
		epoch := ctx.num("timeline", "epoch_unix_ms")
		now := func() int64 { return time.Now().UnixMilli() - epoch }
		if r := e.do(req{method: "POST", url: base + "/prompt-started", headers: agent.headers(), body: map[string]any{"activation_id": act, "at_ms": now()}}); r.status != 200 {
			t.Fatalf("prompt-started: %d %s", r.status, r.raw)
		}
		time.Sleep(20 * time.Millisecond)
		key := fmt.Sprintf("answer-%06d", i)
		if r := seg(key, now()-5, now(), "synthetic answer"); r.status != 201 {
			t.Fatalf("answer segment: %d %s", r.status, r.raw)
		}
		ctx = e.do(req{method: "GET", url: base + "/context", headers: agent.headers()})
		r := e.do(req{method: "POST", url: base + "/answer-complete", headers: agent.headers(), body: map[string]any{
			"state_version": ctx.num("state_version"), "activation_id": act, "flushed_segment_keys": []string{key}, "cause": "agent_tool"}})
		if r.status != 200 {
			t.Fatalf("answer-complete: %d %s", r.status, r.raw)
		}
	}
	view := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie})
	if view.str("state") != "INTERVIEW_COMPLETED" || view.num("progress", "completed_items") != 4 || view.header.Get("X-ATI-Environment") != "test" {
		t.Fatalf("final view: %s %v", view.raw, view.header)
	}
	if c := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/complete", cookie: cookie}); c.status != 200 {
		t.Fatalf("complete confirmation: %d %s", c.status, c.raw)
	}
	for _, forbidden := range []string{"score", "rubric", "evaluation", "candidate_ref"} {
		if strings.Contains(strings.ToLower(string(view.raw)), forbidden) {
			t.Fatalf("candidate view exposes %q: %s", forbidden, view.raw)
		}
	}
	assertNoSecretsInLogs(t, e, cookie, agent.grant)
}

func assertNoSecretsInLogs(t *testing.T, e *env, secrets ...string) {
	t.Helper()
	logs := e.logs.String()
	for _, s := range append(secrets, e.staff, agentToken, "synthetic answer") {
		if s != "" && strings.Contains(logs, s) {
			t.Fatalf("secret or transcript content leaked into logs: %q", s[:min(8, len(s))])
		}
	}
}

func TestCandidateIsolationAndTenantScoping(t *testing.T) {
	e := newEnv(t)
	idA, cookieA := e.interviewToReady()
	idB, _ := e.interviewToReady()
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + idB, cookie: cookieA}); r.status != 404 {
		t.Fatalf("candidate A read B: %d", r.status)
	}
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + idB + "/withdraw", cookie: cookieA, body: map[string]any{"state_version": 3, "confirm": true}}); r.status != 404 {
		t.Fatalf("candidate A withdrew B: %d", r.status)
	}
	// Another tenant's staff token cannot see the interview.
	other := e.otherTenantStaff()
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + idA, headers: map[string]string{"Authorization": "Bearer " + other}}); r.status != 404 {
		t.Fatalf("cross-tenant read: %d", r.status)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + idA}); r.status != 401 {
		t.Fatalf("anonymous read: %d", r.status)
	}
	staffView := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + idA, headers: map[string]string{"Authorization": "Bearer " + e.staff}})
	if staffView.status != 200 || staffView.str("candidate_ref") == "" {
		t.Fatalf("staff view: %d %s", staffView.status, staffView.raw)
	}
}

func (e *env) otherTenantStaff() string {
	e.t.Helper()
	tenant := "ten_other0000000000000000"
	token := "ati_st_other-tenant-token-000000000000000000"
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.tenants (id, name) VALUES ($1,'Other')`, tenant); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ('stf_other000000000000000',$1,encode(sha256($2::bytea),'hex'),'recruiter','o',now()+interval '1 day')`, tenant, token); err != nil {
		e.t.Fatal(err)
	}
	return token
}

func TestCSRFAndListenerSeparation(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/start", cookie: cookie, headers: map[string]string{"Origin": "https://evil.example"}, body: map[string]any{"state_version": 3, "input_mode": "voice"}})
	if r.status != 403 || r.str("code") != "origin_mismatch" {
		t.Fatalf("cross-origin mutation: %d %s", r.status, r.raw)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/internal/v1/tier1/sessions/" + id + "/context", headers: map[string]string{"Authorization": "Bearer " + agentToken}}); r.status != 404 {
		t.Fatalf("public listener must not serve internal routes: %d", r.status)
	}
	if r := e.do(req{method: "GET", url: e.internal.URL + "/api/v1/interviews/" + id, cookie: cookie}); r.status != 404 {
		t.Fatalf("internal listener must not serve public routes: %d", r.status)
	}
	if r := e.do(req{method: "GET", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/context", cookie: cookie}); r.status != 401 {
		t.Fatalf("candidate cookie on internal API: %d", r.status)
	}
}

func TestIdempotencyKeys(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	url := e.public.URL + "/api/v1/interviews/" + id + "/start"
	body := map[string]any{"state_version": 3, "input_mode": "voice"}
	first := e.do(req{method: "POST", url: url, cookie: cookie, body: body, headers: map[string]string{"Idempotency-Key": "fixed-key-0001"}})
	again := e.do(req{method: "POST", url: url, cookie: cookie, body: body, headers: map[string]string{"Idempotency-Key": "fixed-key-0001"}})
	if first.status != 200 || again.status != 200 || again.header.Get("Idempotent-Replayed") != "true" || again.str("state") != "CALIBRATION" {
		t.Fatalf("replay: %d/%d %s", first.status, again.status, again.raw)
	}
	other := e.do(req{method: "POST", url: url, cookie: cookie, body: map[string]any{"state_version": 3, "input_mode": "text"}, headers: map[string]string{"Idempotency-Key": "fixed-key-0001"}})
	if other.status != 409 || other.str("code") != "idempotency_key_reused" {
		t.Fatalf("key reuse with different body: %d %s", other.status, other.raw)
	}
	missing := e.do(req{method: "POST", url: url, cookie: cookie, body: body, headers: map[string]string{"Idempotency-Key": "x"}})
	if missing.status != 400 || missing.str("code") != "idempotency_key_required" {
		t.Fatalf("malformed key: %d %s", missing.status, missing.raw)
	}
}

func TestClaimRequiresRealDispatchAndAgentParticipant(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/start", cookie: cookie, body: map[string]any{"state_version": 3, "input_mode": "voice"}})
	rt := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie})
	room := rt.str("room")
	ds, _ := e.media.ListDispatches(context.Background(), room)
	var meta map[string]string
	_ = json.Unmarshal([]byte(ds[0].Metadata), &meta)
	claim := func(nonce, identity, token string) response {
		return e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/claims", headers: map[string]string{"Authorization": "Bearer " + token},
			body: map[string]any{"session_id": id, "claim_nonce": nonce, "job_id": "AJ_x", "agent_identity": identity}})
	}
	if r := claim(meta["claim_nonce"], "agent-AJ_x", "wrong-service-token"); r.status != 401 {
		t.Fatalf("claim without service credential: %d", r.status)
	}
	if r := claim(meta["claim_nonce"], "agent-AJ_x", agentToken); r.status != 410 {
		t.Fatalf("claim by an identity that is not in the room: %d %s", r.status, r.raw)
	}
	e.media.Join(room, "impostor", false)
	if r := claim(meta["claim_nonce"], "impostor", agentToken); r.status != 410 {
		t.Fatalf("claim by a non-agent participant: %d", r.status)
	}
	e.media.Join(room, "agent-AJ_x", true)
	if r := claim("wrong-nonce-wrong-nonce-wrong-nonce-0000", "agent-AJ_x", agentToken); r.status != 410 {
		t.Fatalf("claim with a forged nonce: %d", r.status)
	}
	if r := claim(meta["claim_nonce"], "agent-AJ_x", agentToken); r.status != 201 {
		t.Fatalf("valid claim: %d %s", r.status, r.raw)
	}
	if r := claim(meta["claim_nonce"], "agent-AJ_x", agentToken); r.status != 410 {
		t.Fatalf("nonce reuse: %d", r.status)
	}
	// A second media request while the lease is active must not dispatch another agent.
	e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie})
	if ds, _ := e.media.ListDispatches(context.Background(), room); len(ds) != 1 {
		t.Fatalf("duplicate dispatch while an agent holds the lease: %d", len(ds))
	}
}

func TestAgentRequestsNeedServiceTokenGrantAndCurrentFencing(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	agent, _ := e.startAndClaim(id, cookie)
	url := e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/context"
	if r := e.do(req{method: "GET", url: url, headers: agent.headers()}); r.status != 200 {
		t.Fatalf("valid lease: %d %s", r.status, r.raw)
	}
	bad := agent.headers()
	bad["Authorization"] = "Bearer not-the-service-token"
	if r := e.do(req{method: "GET", url: url, headers: bad}); r.status != 401 {
		t.Fatalf("missing service credential: %d", r.status)
	}
	stale := agent
	stale.fencing++
	if r := e.do(req{method: "GET", url: url, headers: stale.headers()}); r.status != 401 {
		t.Fatalf("wrong fencing token: %d", r.status)
	}
	// Another session's id with this grant is refused.
	id2, cookie2 := e.interviewToReady()
	e.startAndClaim(id2, cookie2)
	if r := e.do(req{method: "GET", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id2 + "/context", headers: agent.headers()}); r.status != 401 {
		t.Fatalf("cross-session grant: %d", r.status)
	}
}

func (e *env) webhook(ev realtime.Webhook, signature string) int {
	e.t.Helper()
	b, _ := json.Marshal(ev)
	r, _ := http.NewRequest("POST", e.internal.URL+"/internal/v1/livekit/webhook", strings.NewReader(string(b)))
	r.Header.Set("Authorization", signature)
	r.Header.Set("Content-Type", "application/webhook+json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestWebhooksAreVerifiedDeduplicatedAndCrossChecked(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.startAndClaim(id, cookie)
	rt := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie})
	room := rt.str("room")
	state := func() string {
		return e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie}).str("state")
	}
	left := realtime.Webhook{ID: "EV_left_1", Event: "participant_left", CreatedAt: time.Now().Unix(), Room: room, ParticipantIdentity: "candidate"}
	if code := e.webhook(left, "forged"); code != 401 {
		t.Fatalf("unsigned webhook: %d", code)
	}
	// The candidate is still in the room (reconnected): no pause.
	e.webhook(left, "fake-valid-signature")
	if s := state(); s != "CALIBRATION" {
		t.Fatalf("pause despite the candidate being present: %s", s)
	}
	e.media.Leave(room, "candidate")
	left.ID = "EV_left_2"
	stale := left
	stale.ID, stale.CreatedAt = "EV_left_stale", time.Now().Add(-10*time.Minute).Unix()
	e.webhook(stale, "fake-valid-signature")
	if s := state(); s != "CALIBRATION" {
		t.Fatalf("stale webhook acted on: %s", s)
	}
	e.webhook(left, "fake-valid-signature")
	if s := state(); s != "PAUSED" {
		t.Fatalf("disconnect must pause: %s", s)
	}
	resume := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/resume", cookie: cookie, body: map[string]any{"state_version": e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie}).num("state_version")}})
	if resume.status != 200 {
		t.Fatalf("resume: %d %s", resume.status, resume.raw)
	}
	e.webhook(left, "fake-valid-signature") // duplicate event id
	if s := state(); s != "CALIBRATION" {
		t.Fatalf("duplicate webhook acted on: %s", s)
	}
	e.media.Join(room, "intruder", false)
	e.webhook(realtime.Webhook{ID: "EV_join_1", Event: "participant_joined", CreatedAt: time.Now().Unix(), Room: room, ParticipantIdentity: "intruder"}, "fake-valid-signature")
	if parts, _ := e.media.ListParticipants(context.Background(), room); len(parts) != 1 {
		t.Fatalf("unexpected participant must be removed: %+v", parts)
	}
}

func TestInvitationExchangeIsRateLimitedAndTextModeHonest(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/text-responses", cookie: cookie, body: map[string]any{"activation_id": "act_0000000000000000", "text": "x"}}); r.status != 501 {
		t.Fatalf("unimplemented text responses must say so: %d", r.status)
	}
	// The limit is 5 exchanges per minute per client address; setup already used one.
	const remaining = 4
	for i := range remaining + 2 {
		r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", body: map[string]any{"token": strings.Repeat("x", 43)}, headers: map[string]string{"Origin": origin}})
		if i >= remaining && r.status != 429 {
			t.Fatalf("attempt %d: want 429, got %d", i, r.status)
		}
		if i < remaining && r.status != 410 {
			t.Fatalf("attempt %d: want 410 for an unknown invitation, got %d", i, r.status)
		}
	}
}

func TestResumeDispatchesAReplacementForALostAgent(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	a, _ := e.startAndClaim(id, cookie)
	rt := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie})
	room := rt.str("room")
	view := func() response {
		return e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie})
	}
	// The agent hits a provider failure: it pauses the interview, releases its lease and leaves.
	p := e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/pause", headers: a.headers(), body: map[string]any{"state_version": view().num("state_version"), "reason": "provider_failure"}})
	if p.status != 200 || p.str("state") != "PAUSED" {
		t.Fatalf("agent pause: %d %s", p.status, p.raw)
	}
	if rel := e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/lease/release", headers: a.headers()}); rel.status != 204 {
		t.Fatalf("release: %d %s", rel.status, rel.raw)
	}
	e.media.Leave(room, "agent-AJ_1")
	before, _ := e.media.ListDispatches(context.Background(), room)
	r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/resume", cookie: cookie, body: map[string]any{"state_version": view().num("state_version")}})
	if r.status != 200 || r.str("state") != "CALIBRATION" {
		t.Fatalf("resume: %d %s", r.status, r.raw)
	}
	after, _ := e.media.ListDispatches(context.Background(), room)
	if len(after) != len(before)+1 {
		t.Fatalf("resume must dispatch a replacement agent: %d -> %d", len(before), len(after))
	}
	// A second resume-equivalent request does not dispatch again while the new dispatch is pending.
	if _, err := e.svc.EnsureAgentDispatch(context.Background(), id, interview.MediaOps{
		RemoveStaleAgents: func(context.Context, string) error { return nil }, Dispatch: e.media.Dispatch,
	}); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.media.ListDispatches(context.Background(), room); len(again) != len(after) {
		t.Fatal("pending dispatch must not be duplicated")
	}
}

func TestRecordingFollowsTheCandidateMicrophoneAndEgressWebhooks(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.startAndClaim(id, cookie)
	room := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie}).str("room")
	now := time.Now().Unix()
	send := func(ev realtime.Webhook) { e.webhook(ev, "fake-valid-signature") }
	send(realtime.Webhook{ID: "EV_tp_1", Event: "track_published", CreatedAt: now, Room: room, ParticipantIdentity: "candidate", TrackSID: "TR_1", TrackSource: "microphone"})
	send(realtime.Webhook{ID: "EV_tp_2", Event: "track_published", CreatedAt: now, Room: room, ParticipantIdentity: "agent-AJ_1", ParticipantAgent: true, TrackSID: "TR_2", TrackSource: "microphone"})
	send(realtime.Webhook{ID: "EV_tp_3", Event: "track_published", CreatedAt: now, Room: room, ParticipantIdentity: "candidate", TrackSID: "TR_3", TrackSource: "screen_share"})
	send(realtime.Webhook{ID: "EV_tu_1", Event: "track_unpublished", CreatedAt: now, Room: room, ParticipantIdentity: "candidate", TrackSID: "TR_1", TrackSource: "microphone"})
	send(realtime.Webhook{ID: "EV_eg_1", Event: "egress_ended", CreatedAt: now, EgressID: "EG_1", EgressStatus: "EGRESS_COMPLETE"})
	send(realtime.Webhook{ID: "EV_eg_1", Event: "egress_ended", CreatedAt: now, EgressID: "EG_1", EgressStatus: "EGRESS_COMPLETE"}) // duplicate
	starts, stops, egress := e.rec.snapshot()
	if len(starts) != 1 || starts[0] != id || len(stops) != 1 || stops[0] != id {
		t.Fatalf("only the candidate's microphone starts and stops recording: starts=%v stops=%v", starts, stops)
	}
	if len(egress) != 1 || egress[0] != "EG_1:EGRESS_COMPLETE" {
		t.Fatalf("egress webhooks reach the recorder once: %v", egress)
	}
}
