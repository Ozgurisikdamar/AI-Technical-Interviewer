package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
)

func (e *env) createInterview(token, key string, ref string) response {
	e.t.Helper()
	h := map[string]string{"Authorization": "Bearer " + token}
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews", headers: h,
		body: map[string]any{"candidate_ref": ref, "plan_id": "plan_demo_backend_en", "plan_version": 1}})
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestStaffTokensExpireAndRevocationTakesEffectImmediately(t *testing.T) {
	e := newEnv(t)
	insert := func(id, expires, revoked string) string {
		token := api.StaffTokenPrefix + ids.Token()
		if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at, revoked_at)
			VALUES ($1,$2,$3,'recruiter','t',now() + $4::interval, now() + $5::interval)`, id, e.tenant, ids.Hash(token), expires, revoked); err != nil {
			t.Fatal(err)
		}
		return token
	}
	expired := insert("stf_expired0000000000000", "-1 second", "1 day")
	revoked := insert("stf_revoked0000000000000", "1 day", "-1 second")
	for name, token := range map[string]string{"expired": expired, "revoked": revoked, "unprefixed": strings.TrimPrefix(e.staff, api.StaffTokenPrefix)} {
		if r := e.createInterview(token, "", ids.New("ref")); r.status != 401 {
			t.Errorf("%s staff token: %d %s", name, r.status, r.raw)
		}
	}
	ok := e.createInterview(e.staff, "", ids.New("ref"))
	if ok.status != 201 {
		t.Fatalf("valid staff token: %d %s", ok.status, ok.raw)
	}
	id := ok.str("interview", "id")
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: map[string]string{"Authorization": "Bearer " + expired}}); r.status != 401 {
		t.Fatalf("expired token read: %d", r.status)
	}
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.staff_tokens SET revoked_at = now() WHERE id = $1`, staffRowID); err != nil {
		t.Fatal(err)
	}
	if r := e.createInterview(e.staff, "", ids.New("ref")); r.status != 401 {
		t.Fatalf("a revoked token must stop working at once: %d", r.status)
	}
}

// BP18-13: concurrent creation never merges different requests and never duplicates one.
func TestConcurrentInterviewCreation(t *testing.T) {
	e := newEnv(t)
	const n = 8
	var wg sync.WaitGroup
	distinct := make([]response, n)
	same := make([]response, n)
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			distinct[i] = e.createInterview(e.staff, fmt.Sprintf("distinct-key-%04d", i), fmt.Sprintf("ref-distinct-%d", i))
		}()
		go func() {
			defer wg.Done()
			same[i] = e.createInterview(e.staff, "same-key-0000001", "ref-same")
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, r := range distinct {
		if r.status != 201 {
			t.Fatalf("distinct %d: %d %s", i, r.status, r.raw)
		}
		seen[r.str("interview", "id")] = true
	}
	if len(seen) != n {
		t.Fatalf("%d distinct requests produced %d interviews", n, len(seen))
	}
	var created, replayed, inProgress int
	ids := map[string]bool{}
	for i, r := range same {
		switch {
		case r.status == 201 && r.header.Get("Idempotent-Replayed") == "true":
			replayed++
			ids[r.str("interview", "id")] = true
		case r.status == 201:
			created++
			ids[r.str("interview", "id")] = true
		case r.status == 409 && r.str("code") == "conflict":
			inProgress++
		default:
			t.Fatalf("same-key request %d: %d %s", i, r.status, r.raw)
		}
	}
	if created != 1 || len(ids) != 1 || created+replayed+inProgress != n {
		t.Fatalf("one creation expected: created=%d replayed=%d in_progress=%d interviews=%v", created, replayed, inProgress, ids)
	}
	if got := e.count(`SELECT count(*) FROM ati.interview_sessions s JOIN ati.candidates c ON c.id = s.candidate_id WHERE c.external_ref = 'ref-same'`); got != 1 {
		t.Fatalf("the same request created %d interviews", got)
	}
}

// The invitation token and media tokens are credentials: they are returned once and never kept at
// rest, not even for idempotent replays (THREAT_MODEL_ADDENDUM_PHASE1: hashed storage).
func TestCredentialsAreNeverStoredForReplays(t *testing.T) {
	e := newEnv(t)
	first := e.createInterview(e.staff, "create-key-00001", "ref-replay")
	token := strings.TrimPrefix(first.str("invitation_url"), origin+"/i#token=")
	if first.status != 201 || len(token) < 32 {
		t.Fatalf("create: %d %s", first.status, first.raw)
	}
	replay := e.createInterview(e.staff, "create-key-00001", "ref-replay")
	if replay.status != 201 || replay.header.Get("Idempotent-Replayed") != "true" || replay.str("interview", "id") != first.str("interview", "id") {
		t.Fatalf("replay: %d %s", replay.status, replay.raw)
	}
	if replay.body["invitation_url"] != nil || strings.Contains(string(replay.raw), token) {
		t.Fatalf("a replay must not return the invitation again: %s", replay.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.invitations WHERE token_hash = $1`, ids.Hash(token)); n != 1 {
		t.Fatal("the invitation is stored by its hash")
	}
	id, cookie := e.interviewToReady()
	agent, _ := e.startAndClaim(id, cookie)
	rt := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie, headers: map[string]string{"Idempotency-Key": "media-key-000001"}})
	again := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie, headers: map[string]string{"Idempotency-Key": "media-key-000001"}})
	if rt.status != 200 || again.status != 200 || again.str("token") == "" || again.str("token") == rt.str("token") {
		t.Fatalf("each media request returns a fresh token: %d %d", rt.status, again.status)
	}
	for _, secret := range []string{token, rt.str("token"), again.str("token"), cookie, agent.grant} {
		if n := e.count(`SELECT count(*) FROM ati.idempotency_records WHERE response::text LIKE '%' || $1 || '%'`, secret); n != 0 {
			t.Fatalf("a credential is stored at rest in idempotency records (%s…)", secret[:8])
		}
		if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE details::text LIKE '%' || $1 || '%'`, secret); n != 0 {
			t.Fatalf("a credential reached the audit log (%s…)", secret[:8])
		}
	}
	assertNoSecretsInLogs(t, e, token, rt.str("token"), cookie, agent.grant)
}

func TestSessionCookieIsSecureOutsideDevAndTest(t *testing.T) {
	e := newEnvWith(t, map[string]string{"APP_ENV": "staging"})
	r := e.createInterview(e.staff, "", ids.New("ref"))
	if r.status != 201 || r.header.Get("X-ATI-Environment") != "staging" {
		t.Fatalf("create: %d %v", r.status, r.header)
	}
	token := strings.TrimPrefix(r.str("invitation_url"), origin+"/i#token=")
	ex := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", body: map[string]any{"token": token}, headers: map[string]string{"Origin": origin}})
	var secure bool
	for _, c := range (&http.Response{Header: ex.header}).Cookies() {
		if c.Name == api.CandidateCookie {
			secure = c.Secure && c.HttpOnly && c.SameSite == http.SameSiteStrictMode
		}
	}
	if ex.status != 200 || !secure {
		t.Fatalf("the session cookie must be Secure, HttpOnly and SameSite=Strict outside dev/test: %d %v", ex.status, ex.header.Values("Set-Cookie"))
	}
}

func (e *env) revoke(id, token string) response {
	e.t.Helper()
	return e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/invitation/revoke", headers: map[string]string{"Authorization": "Bearer " + token}})
}

func TestStaffCanRevokeAnUnusedInvitation(t *testing.T) {
	e := newEnv(t)
	r := e.createInterview(e.staff, "", ids.New("ref"))
	id := r.str("interview", "id")
	token := strings.TrimPrefix(r.str("invitation_url"), origin+"/i#token=")
	if other := e.revoke(id, e.otherTenantStaff()); other.status != 404 {
		t.Fatalf("another tenant revoked the invitation: %d", other.status)
	}
	if anon := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/invitation/revoke", headers: map[string]string{"Origin": origin}}); anon.status != 401 {
		t.Fatalf("anonymous revocation: %d", anon.status)
	}
	rv := e.revoke(id, e.staff)
	if rv.status != 200 || rv.num("revoked", "invitations") != 1 || rv.body["revoked"].(map[string]any)["paused"] != false || rv.str("interview", "state") != "CREATED" {
		t.Fatalf("revoke: %d %s", rv.status, rv.raw)
	}
	ex := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", body: map[string]any{"token": token}, headers: map[string]string{"Origin": origin}})
	if ex.status != 410 || ex.str("code") != "invitation_invalid" {
		t.Fatalf("a revoked invitation must not be exchanged: %d %s", ex.status, ex.raw)
	}
	again := e.revoke(id, e.staff)
	if again.status != 200 || again.num("revoked", "invitations") != 0 {
		t.Fatalf("repeated revocation changes nothing: %d %s", again.status, again.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'invitation.revoke' AND actor_id = $2`, id, staffRowID); n != 2 {
		t.Fatalf("revocations are audited with the staff identity: %d", n)
	}
}

func TestRevokingALiveInterviewPausesItAndDeletesTheRoom(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.startAndClaim(id, cookie)
	room := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie}).str("room")
	rv := e.revoke(id, e.staff)
	if rv.status != 200 || rv.str("interview", "state") != "PAUSED" {
		t.Fatalf("revoke: %d %s", rv.status, rv.raw)
	}
	if got := rv.body["revoked"].(map[string]any); got["paused"] != true || got["room_deleted"] != true || got["candidate_sessions"] != float64(1) {
		t.Fatalf("revocation outcome: %v", got)
	}
	if e.count(`SELECT count(*) FROM ati.pause_intervals WHERE session_id = $1 AND reason = 'access_revoked' AND ended_ms IS NULL`, id) != 1 {
		t.Fatal("the pause records why the candidate lost access")
	}
	deleted := false
	for _, d := range e.media.Deleted {
		deleted = deleted || d == room
	}
	if !deleted || e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'media.room_deleted' AND details->>'reason' = 'access_revoked'`, id) != 1 {
		t.Fatalf("the room is deleted at once (connected tokens cannot be revoked): %v", e.media.Deleted)
	}
	for _, path := range []string{"", "/resume"} {
		method := map[string]string{"": "GET", "/resume": "POST"}[path]
		if r := e.do(req{method: method, url: e.public.URL + "/api/v1/interviews/" + id + path, cookie: cookie, body: map[string]any{"state_version": rv.num("interview", "state_version")}}); r.status != 401 {
			t.Fatalf("%s %s with the revoked cookie: %d", method, path, r.status)
		}
	}
	// Nobody can resume, so the pause expires and the interview is cancelled without a completion event.
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.interview_sessions SET paused_at = now() - interval '2 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ExpirePausedSessions(context.Background()); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	staffView := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: map[string]string{"Authorization": "Bearer " + e.staff}})
	if staffView.str("state") != "CANCELLED" || e.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1 AND event_type = 'interview.completed'`, id) != 0 {
		t.Fatalf("after expiry: %s", staffView.raw)
	}
}

// Threat model: an expired pause deletes the room; ADR-015: a cancelled interview emits no
// completion event.
func TestPauseExpiryDeletesTheRoomAndEmitsNoCompletion(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.startAndClaim(id, cookie)
	room := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie}).str("room")
	v := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie}).num("state_version")
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/pause", cookie: cookie, body: map[string]any{"state_version": v, "reason": "candidate_request"}}); r.status != 200 {
		t.Fatalf("pause: %d %s", r.status, r.raw)
	}
	before := len(e.media.Deleted)
	if n, err := e.svc.ExpirePausedSessions(context.Background()); err != nil || n != 0 {
		t.Fatalf("a fresh pause must not expire: %d %v", n, err)
	}
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.interview_sessions SET paused_at = now() - interval '31 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ExpirePausedSessions(context.Background()); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	if len(e.media.Deleted) != before+1 || e.media.Deleted[before] != room {
		t.Fatalf("pause expiry must delete the room: %v", e.media.Deleted)
	}
	view := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie})
	if view.str("state") != "CANCELLED" {
		t.Fatalf("state %s", view.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1 AND event_type = 'interview.completed'`, id); n != 0 {
		t.Fatalf("a cancelled interview emitted %d completion events", n)
	}
}

func TestConsentDeclineCancelsWithoutAnyHandoffEvent(t *testing.T) {
	e := newEnv(t)
	r := e.createInterview(e.staff, "", ids.New("ref"))
	token := strings.TrimPrefix(r.str("invitation_url"), origin+"/i#token=")
	ex := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", body: map[string]any{"token": token}, headers: map[string]string{"Origin": origin}})
	id, cookie := ex.str("interview_id"), candidateCookie(t, ex)
	c := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/consent", cookie: cookie, body: map[string]any{
		"notice_id": "notice_default", "notice_version": "2026-11-draft.1",
		"decisions": []map[string]any{{"purpose_id": "voice_interview_processing", "granted": true}, {"purpose_id": "transcript_assessment_record", "granted": false},
			{"purpose_id": "ai_assisted_evaluation_with_human_review", "granted": true}, {"purpose_id": "audio_recording", "granted": false}}}})
	if c.status != 200 || c.str("state") != "CANCELLED" {
		t.Fatalf("declining a required purpose cancels: %d %s", c.status, c.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.outbox_events WHERE session_id = $1`, id); n != 0 {
		t.Fatalf("a declined interview emitted %d outbox events", n)
	}
	if n := e.count(`SELECT count(*) FROM ati.consent_records WHERE session_id = $1`, id); n != 4 {
		t.Fatalf("every decision is recorded, including the refusal: %d", n)
	}
}

func assertHardened(t *testing.T, name string, h http.Header, env string) {
	t.Helper()
	want := map[string]string{
		"X-ATI-Environment": env, "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
		"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY",
	}
	for k, v := range want {
		if h.Get(k) != v {
			t.Errorf("%s: %s = %q, want %q", name, k, h.Get(k), v)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("%s: Content-Security-Policy = %q", name, csp)
	}
	if h.Get("X-Request-ID") == "" {
		t.Errorf("%s: no X-Request-ID", name)
	}
}

// Every response on both listeners carries the environment marker and the hardening headers,
// including errors and unknown paths (ADR-016, ADR-020).
func TestEveryResponseIsMarkedAndHardened(t *testing.T) {
	e := newEnv(t)
	cases := map[string]req{
		"public success":   {method: "POST", url: e.public.URL + "/api/v1/interviews", headers: map[string]string{"Authorization": "Bearer " + e.staff}, body: map[string]any{"candidate_ref": "r", "plan_id": "plan_demo_backend_en", "plan_version": 1}},
		"public problem":   {method: "GET", url: e.public.URL + "/api/v1/interviews/ses_nope"},
		"public 404":       {method: "GET", url: e.public.URL + "/no/such/path"},
		"internal health":  {method: "GET", url: e.internal.URL + "/healthz"},
		"internal problem": {method: "GET", url: e.internal.URL + "/internal/v1/tier1/sessions/ses_x/context"},
		"internal 404":     {method: "GET", url: e.internal.URL + "/api/v1/interviews"},
		"metrics":          {method: "GET", url: e.internal.URL + "/metrics"},
	}
	for name, q := range cases {
		r := e.do(q)
		assertHardened(t, name, r.header, "test")
		if strings.Contains(name, "problem") || strings.Contains(name, "404") {
			if !strings.HasPrefix(r.header.Get("Content-Type"), "application/problem+json") || r.str("code") == "" {
				t.Errorf("%s: %d %s %s", name, r.status, r.header.Get("Content-Type"), r.raw)
			}
		}
	}
}

func TestAgentTelemetryReachesPrometheus(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	agent, _ := e.startAndClaim(id, cookie)
	url := e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/telemetry"
	ok := e.do(req{method: "POST", url: url, headers: agent.headers(), body: map[string]any{
		"metrics": []map[string]any{{"name": "voice_end_of_turn_ms", "value_ms": 640}, {"name": "barge_in_stop_latency_ms", "value_ms": 410}}, "policy_flags": []any{}}})
	if ok.status != 202 {
		t.Fatalf("telemetry: %d %s", ok.status, ok.raw)
	}
	bad := e.do(req{method: "POST", url: url, headers: agent.headers(), body: map[string]any{"metrics": []map[string]any{{"name": "candidate_stress_level", "value_ms": 1}}, "policy_flags": []any{}}})
	if bad.status != 400 {
		t.Fatalf("an unknown metric must be refused: %d", bad.status)
	}
	if r := e.do(req{method: "POST", url: url, body: map[string]any{"metrics": []any{}, "policy_flags": []any{}}}); r.status != 401 {
		t.Fatalf("telemetry without a lease: %d", r.status)
	}
	resp, err := http.Get(e.internal.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	text := b.String()
	for _, want := range []string{`metric="voice_end_of_turn_ms"`, `metric="barge_in_stop_latency_ms"`} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
	if strings.Contains(text, "candidate_stress_level") || strings.Contains(text, id) {
		t.Error("metrics must not carry refused names or session identifiers")
	}
}

// Claim nonces expire: a dispatch that nobody claimed in time cannot be claimed later (ADR-016).
func TestExpiredClaimNoncesAreRefused(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/start", cookie: cookie, body: map[string]any{"state_version": 3, "input_mode": "voice"}})
	room := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie}).str("room")
	ds, _ := e.media.ListDispatches(context.Background(), room)
	nonce := strings.Split(strings.Split(ds[0].Metadata, `"claim_nonce":"`)[1], `"`)[0]
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.agent_dispatches SET nonce_expires_at = now() - interval '1 second' WHERE session_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	e.media.Join(room, "agent-AJ_late", true)
	claim := e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/claims", headers: map[string]string{"Authorization": "Bearer " + agentToken},
		body: map[string]any{"session_id": id, "claim_nonce": nonce, "job_id": "AJ_late", "agent_identity": "agent-AJ_late"}})
	if claim.status != 410 {
		t.Fatalf("an expired nonce was accepted: %d %s", claim.status, claim.raw)
	}
	if e.count(`SELECT count(*) FROM ati.agent_leases WHERE session_id = $1`, id) != 0 {
		t.Fatal("no lease may be issued for an expired nonce")
	}
}

// Threat model (B10): an API key holder can mint an agent-kind token. While an agent holds the
// lease no other is dispatched, so a second agent participant is removed and audited; the lease
// holder reconnecting, or a replacement joining when no lease is held, is kept.
func TestASecondAgentWhileOneHoldsTheLeaseIsRemoved(t *testing.T) {
	e := newEnv(t)
	id, cookie := e.interviewToReady()
	a, _ := e.startAndClaim(id, cookie)
	room := e.do(req{method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/realtime-session", cookie: cookie}).str("room")
	present := func(identity string) bool {
		parts, _ := e.media.ListParticipants(context.Background(), room)
		for _, p := range parts {
			if p.Identity == identity {
				return true
			}
		}
		return false
	}
	join := func(eventID, identity string) {
		e.media.Join(room, identity, true)
		e.webhook(realtime.Webhook{ID: eventID, Event: "participant_joined", CreatedAt: time.Now().Unix(), Room: room, ParticipantIdentity: identity, ParticipantAgent: true}, "fake-valid-signature")
	}
	join("EV_agent_spy", "agent-eavesdropper")
	if present("agent-eavesdropper") || !present("agent-AJ_1") {
		t.Fatal("a second agent while the lease is held must be removed, and only it")
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE subject_id = $1 AND action = 'media.unexpected_participant'`, id); n != 1 {
		t.Fatalf("the removal must be audited: %d", n)
	}
	e.webhook(realtime.Webhook{ID: "EV_agent_rejoin", Event: "participant_joined", CreatedAt: time.Now().Unix(), Room: room, ParticipantIdentity: "agent-AJ_1", ParticipantAgent: true}, "fake-valid-signature")
	if !present("agent-AJ_1") {
		t.Fatal("the lease holder reconnecting is kept")
	}
	if r := e.do(req{method: "POST", url: e.internal.URL + "/internal/v1/tier1/sessions/" + id + "/lease/release", headers: a.headers()}); r.status != 204 {
		t.Fatalf("release: %d", r.status)
	}
	join("EV_agent_next", "agent-AJ_2")
	if !present("agent-AJ_2") {
		t.Fatal("with no lease held, a joining agent may be the dispatched replacement and is kept")
	}
}

// The request method is client-controlled: arbitrary methods must not mint metric label values.
func TestHTTPMetricsBoundTheMethodLabel(t *testing.T) {
	e := newEnv(t)
	for _, m := range []string{"BREW", "PROPFIND", "X-" + strings.Repeat("A", 40)} {
		r, _ := http.NewRequest(m, e.public.URL+"/api/v1/interviews", nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(e.internal.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	if strings.Contains(text, "BREW") || strings.Contains(text, "PROPFIND") || strings.Contains(text, "X-AAAA") {
		t.Fatal("client-chosen methods leaked into metric labels")
	}
	if !strings.Contains(text, `method="other"`) {
		t.Fatal(`unknown methods are counted as method="other"`)
	}
}

// A claim whose request never finished (the process stopped mid-request) blocks its key only until
// it is stale; a recent claim still means "in progress".
func TestAStaleIdempotencyClaimCanBeTakenOver(t *testing.T) {
	e := newEnv(t)
	first := e.createInterview(e.staff, "crash-key-000001", "ref-crash")
	if first.status != 201 {
		t.Fatalf("create: %d %s", first.status, first.raw)
	}
	// Simulate a request that claimed the key and never recorded an outcome.
	unfinished := func(age string) {
		if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.idempotency_records SET status = NULL, response = NULL, claimed_at = now() - $1::interval
			WHERE idem_key = 'crash-key-000001'`, age); err != nil {
			t.Fatal(err)
		}
	}
	unfinished("10 seconds")
	if r := e.createInterview(e.staff, "crash-key-000001", "ref-crash"); r.status != 409 || r.str("code") != "conflict" {
		t.Fatalf("a recent claim is in progress: %d %s", r.status, r.raw)
	}
	unfinished("10 minutes")
	if r := e.createInterview(e.staff, "crash-key-000001", "ref-crash"); r.status != 201 || r.header.Get("Idempotent-Replayed") == "true" {
		t.Fatalf("a stale claim is taken over: %d %s", r.status, r.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.idempotency_records WHERE idem_key = 'crash-key-000001' AND status = 201`); n != 1 {
		t.Fatal("the takeover records its outcome")
	}
}
