package api_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
)

// signIn posts a staff token to the sign-in endpoint as the review console does (same origin).
func (e *env) signIn(token string, headers map[string]string) response {
	e.t.Helper()
	h := map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-origin"}
	for k, v := range headers {
		h[k] = v
	}
	return e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session", headers: h, body: map[string]any{"token": token}})
}

// staffCookie extracts the staff session cookie from a sign-in response and checks its attributes.
func staffCookie(t *testing.T, r response) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.StaffCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api" || c.Secure {
				t.Fatalf("cookie attributes: %+v", c)
			}
			return c
		}
	}
	t.Fatalf("no staff session cookie in %v", r.header)
	return nil
}

// asSession sends the staff session cookie the way a browser on the console's origin would.
func asSession(cookie string) map[string]string {
	return map[string]string{"Cookie": api.StaffCookie + "=" + cookie, "Origin": origin, "Sec-Fetch-Site": "same-origin"}
}

func TestStaffSignInIssuesAShortLivedHttpOnlySession(t *testing.T) {
	e := newEnv(t)
	reviewer := e.staffToken("reviewer")
	r := e.signIn(reviewer, nil)
	if r.status != 200 || r.str("role") != "reviewer" || r.str("label") != "test" {
		t.Fatalf("sign-in: %d %s", r.status, r.raw)
	}
	c := staffCookie(t, r)
	if c.MaxAge < int((8*time.Hour-time.Minute)/time.Second) || c.MaxAge > int(8*time.Hour/time.Second) {
		t.Fatalf("the session lasts the configured 8h: max-age %d", c.MaxAge)
	}
	expires, err := time.Parse(time.RFC3339Nano, r.str("expires_at"))
	if err != nil || time.Until(expires) > 8*time.Hour || time.Until(expires) < 8*time.Hour-time.Minute {
		t.Fatalf("expires_at %q: %v", r.str("expires_at"), err)
	}
	// Only the hash is stored, and no credential reaches the audit log.
	if n := e.count(`SELECT count(*) FROM ati.staff_sessions WHERE token_hash = $1 AND staff_token_id IN (SELECT id FROM ati.staff_tokens WHERE token_hash = $2)`, ids.Hash(c.Value), ids.Hash(reviewer)); n != 1 {
		t.Fatalf("stored sessions: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM ati.staff_sessions s WHERE s::text LIKE '%' || $1 || '%'`, c.Value); n != 0 {
		t.Fatal("the session token must be stored only as a hash")
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'staff.session.start' AND subject_type = 'staff_session' AND details->>'role' = 'reviewer'`); n != 1 {
		t.Fatalf("sign-in audits: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE details::text LIKE '%' || $1 || '%' OR details::text LIKE '%' || $2 || '%' OR details::text LIKE '%' || $3 || '%' OR details::text LIKE '%"test"%'`,
		c.Value, reviewer, ids.Hash(c.Value)); n != 0 {
		t.Fatal("audit details must hold no token, hash or label")
	}
	who := e.do(req{method: "GET", url: e.public.URL + "/api/v1/staff/session", headers: asSession(c.Value)})
	if who.status != 200 || who.str("role") != "reviewer" || who.str("expires_at") != r.str("expires_at") {
		t.Fatalf("who am I: %d %s", who.status, who.raw)
	}
	if who := e.do(req{method: "GET", url: e.public.URL + "/api/v1/staff/session", headers: map[string]string{"Authorization": "Bearer " + reviewer}}); who.status != 200 || who.str("role") != "reviewer" {
		t.Fatalf("who am I with the bearer token: %d %s", who.status, who.raw)
	}
	if who := e.do(req{method: "GET", url: e.public.URL + "/api/v1/staff/session"}); who.status != 401 {
		t.Fatalf("who am I without credentials: %d", who.status)
	}
}

func TestStaffSignInRefusesBadTokensAlikeAndIsRateLimited(t *testing.T) {
	e := newEnvWith(t, map[string]string{"ATI_STAFF_SIGNINS_PER_MINUTE": "8"})
	expired, revoked := api.StaffTokenPrefix+ids.Token(), api.StaffTokenPrefix+ids.Token()
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at, revoked_at) VALUES
		($1,$2,$3,'reviewer','e',now() - interval '1 second',NULL), ($4,$2,$5,'reviewer','r',now() + interval '1 day',now())`,
		ids.New(ids.Staff), e.tenant, ids.Hash(expired), ids.New(ids.Staff), ids.Hash(revoked)); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"malformed": "not-a-staff-token", "unknown": api.StaffTokenPrefix + ids.Token(), "expired": expired, "revoked": revoked,
		"agent credential": agentToken, "too long": api.StaffTokenPrefix + strings.Repeat("a", 200),
	} {
		r := e.signIn(token, nil)
		if r.status != 401 || r.str("code") != "unauthorized" || r.str("detail") != "the staff token is invalid, expired or revoked" {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
		if len((&http.Response{Header: r.header}).Cookies()) != 0 {
			t.Errorf("%s: a refused sign-in sets no cookie", name)
		}
	}
	if n := e.count(`SELECT count(*) FROM ati.staff_sessions`); n != 0 {
		t.Fatalf("refused sign-ins created %d sessions", n)
	}
	// Login CSRF: a request from another site, or without an Origin, is refused before the token is looked at.
	if r := e.signIn(e.staff, map[string]string{"Origin": "https://evil.example"}); r.status != 403 || r.str("code") != "origin_mismatch" {
		t.Fatalf("cross-origin sign-in: %d %s", r.status, r.raw)
	}
	if r := e.signIn(e.staff, map[string]string{"Sec-Fetch-Site": "cross-site"}); r.status != 403 {
		t.Fatalf("cross-site sign-in: %d %s", r.status, r.raw)
	}
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session", headers: map[string]string{"Origin": origin, "Idempotency-Key": "x"}, body: map[string]any{"token": e.staff}}); r.status != 400 || r.str("code") != "idempotency_key_required" {
		t.Fatalf("without a valid Idempotency-Key: %d %s", r.status, r.raw)
	}
	// Eight attempts per minute and client, counting the seven refused by the handler above (the
	// same-origin guard refuses before the limiter is consulted).
	if r := e.signIn(e.staff, nil); r.status != 200 {
		t.Fatalf("eighth attempt: %d %s", r.status, r.raw)
	}
	if r := e.signIn(e.staff, nil); r.status != 429 || r.str("code") != "rate_limited" {
		t.Fatalf("ninth attempt: %d %s", r.status, r.raw)
	}
}

func TestStaffSessionIsAcceptedOnlyForReviewReportsAndItself(t *testing.T) {
	e := newEnv(t)
	recruiter := staffCookie(t, e.signIn(e.staff, nil)).Value
	reviewerToken := e.staffToken("reviewer")
	reviewer := staffCookie(t, e.signIn(reviewerToken, nil)).Value
	id, candidate := e.interviewToReady()
	for name, q := range map[string]req{
		"create an interview":   {method: "POST", url: e.public.URL + "/api/v1/interviews", headers: asSession(recruiter), body: map[string]any{"candidate_ref": "r", "plan_id": "plan_demo_backend_en", "plan_version": 1}},
		"revoke an invitation":  {method: "POST", url: e.public.URL + "/api/v1/interviews/" + id + "/invitation/revoke", headers: asSession(recruiter)},
		"read an interview":     {method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: asSession(recruiter)},
		"a candidate operation": {method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/consent-notice", headers: asSession(recruiter)},
	} {
		if r := e.do(q); r.status != 401 {
			t.Errorf("%s with the browser session: %d %s", name, r.status, r.raw)
		}
	}
	for name, q := range map[string]req{
		"the queue":   {method: "GET", url: e.public.URL + "/api/v1/review-tasks", headers: asSession(reviewer)},
		"the reports": {method: "GET", url: e.public.URL + "/api/v1/reports", headers: asSession(recruiter)},
	} {
		if r := e.do(q); r.status != 200 {
			t.Errorf("%s with the browser session: %d %s", name, r.status, r.raw)
		}
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks", headers: asSession(recruiter)}); r.status != 403 {
		t.Fatalf("roles apply to sessions too: %d", r.status)
	}
	// An Authorization header alone decides when present: a bad one is not rescued by the cookie.
	bad := asSession(reviewer)
	bad["Authorization"] = "Bearer " + agentToken
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks", headers: bad}); r.status != 401 {
		t.Fatalf("a bad bearer with a valid session: %d", r.status)
	}
	// A browser holding both cookies still gets the candidate view: the staff session is ignored there.
	both := map[string]string{"Cookie": api.CandidateCookie + "=" + candidate + "; " + api.StaffCookie + "=" + recruiter}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: both}); r.status != 200 || r.str("state") != "READY" || r.body["candidate_ref"] != nil {
		t.Fatalf("candidate view with both cookies: %d %s", r.status, r.raw)
	}
	// Reviewers never see who the candidate is (ADR-022): the staff view names the candidate.
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: map[string]string{"Authorization": "Bearer " + reviewerToken}}); r.status != 403 {
		t.Fatalf("a reviewer reads the staff view: %d %s", r.status, r.raw)
	}
	if r := e.createInterview(reviewerToken, "", ids.New("ref")); r.status != 403 {
		t.Fatalf("a reviewer creates an interview: %d %s", r.status, r.raw)
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: map[string]string{"Authorization": "Bearer " + e.staffToken("admin")}}); r.status != 200 || r.str("candidate_ref") == "" {
		t.Fatalf("an administrator reads the staff view: %d %s", r.status, r.raw)
	}
}

func TestStaffSessionsEndWithSignOutReplacementAndTheirToken(t *testing.T) {
	e := newEnv(t)
	first := staffCookie(t, e.signIn(e.staff, nil)).Value
	// Signing in again from the same browser replaces its session.
	second := staffCookie(t, e.signIn(e.staff, map[string]string{"Cookie": api.StaffCookie + "=" + first})).Value
	who := func(c string) int {
		return e.do(req{method: "GET", url: e.public.URL + "/api/v1/staff/session", headers: asSession(c)}).status
	}
	if who(first) != 401 || who(second) != 200 {
		t.Fatalf("after signing in again: first %d, second %d", who(first), who(second))
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'staff.session.start' AND details->>'replaced_session' = 'true'`); n != 1 {
		t.Fatalf("replacement audits: %d", n)
	}
	// Signing out needs the console's origin, like every cookie-authenticated change.
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session/end", headers: map[string]string{"Cookie": api.StaffCookie + "=" + second}}); r.status != 403 {
		t.Fatalf("sign-out without an Origin: %d", r.status)
	}
	out := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session/end", headers: asSession(second)})
	if out.status != 200 || out.body["ended"] != true {
		t.Fatalf("sign-out: %d %s", out.status, out.raw)
	}
	cleared := false
	for _, c := range (&http.Response{Header: out.header}).Cookies() {
		cleared = cleared || (c.Name == api.StaffCookie && c.MaxAge < 0 && c.Value == "")
	}
	if !cleared || who(second) != 401 {
		t.Fatalf("after sign-out: cleared=%v, status %d", cleared, who(second))
	}
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session/end", headers: asSession(second)}); r.status != 401 {
		t.Fatalf("signing out twice: %d", r.status)
	}
	if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session/end", headers: map[string]string{"Authorization": "Bearer " + e.staff}}); r.status != 200 || r.body["ended"] != false {
		t.Fatalf("a bearer token has no session to end: %d %s", r.status, r.raw)
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'staff.session.end'`); n != 1 {
		t.Fatalf("sign-out audits: %d", n)
	}
	// Revoking the staff token ends its sessions at once.
	third := staffCookie(t, e.signIn(e.staff, nil)).Value
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.staff_tokens SET revoked_at = now() WHERE id = $1`, staffRowID); err != nil {
		t.Fatal(err)
	}
	if who(third) != 401 {
		t.Fatal("a session must end with its staff token")
	}
}

func TestStaffSessionsNeverOutliveTheirToken(t *testing.T) {
	e := newEnv(t)
	token := api.StaffTokenPrefix + ids.Token()
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,'reviewer','short',now() + interval '20 minutes')`,
		ids.New(ids.Staff), e.tenant, ids.Hash(token)); err != nil {
		t.Fatal(err)
	}
	r := e.signIn(token, nil)
	c := staffCookie(t, r)
	expires, _ := time.Parse(time.RFC3339Nano, r.str("expires_at"))
	if d := time.Until(expires); d > 20*time.Minute || d < 19*time.Minute || c.MaxAge > 20*60 || c.MaxAge < 19*60 {
		t.Fatalf("the session ends with the token: expires in %v, max-age %d", d, c.MaxAge)
	}
	// A token about to expire is refused rather than producing a session that ends at once.
	dying := api.StaffTokenPrefix + ids.Token()
	if _, err := e.db.Owner.Exec(context.Background(), `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,'reviewer','dying',now() + interval '30 seconds')`,
		ids.New(ids.Staff), e.tenant, ids.Hash(dying)); err != nil {
		t.Fatal(err)
	}
	if r := e.signIn(dying, nil); r.status != 401 || len((&http.Response{Header: r.header}).Cookies()) != 0 {
		t.Fatalf("a token with 30 s left: %d %s", r.status, r.raw)
	}
}

// Sign-out with a bearer token ends no session, so it must not clear a session cookie sent alongside:
// that would look like a sign-out while the session stays valid.
func TestSigningOutWithABearerTokenLeavesTheSessionCookieAlone(t *testing.T) {
	e := newEnv(t)
	cookie := staffCookie(t, e.signIn(e.staff, nil)).Value
	both := asSession(cookie)
	both["Authorization"] = "Bearer " + e.staff
	r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/staff/session/end", headers: both})
	if r.status != 200 || r.body["ended"] != false || len((&http.Response{Header: r.header}).Cookies()) != 0 {
		t.Fatalf("bearer sign-out: %d %s %v", r.status, r.raw, r.header.Values("Set-Cookie"))
	}
	if who := e.do(req{method: "GET", url: e.public.URL + "/api/v1/staff/session", headers: asSession(cookie)}); who.status != 200 {
		t.Fatalf("the session must still be valid: %d", who.status)
	}
}

// A failure while authenticating (here: the database is gone) is a server error, never a 401: the
// console treats 401 as "signed out", and the candidate page as "session lost".
func TestAnOutageIsNeverMistakenForASignedOutUser(t *testing.T) {
	e := newEnv(t)
	id, candidate := e.interviewToReady()
	session := staffCookie(t, e.signIn(e.staff, nil)).Value
	e.pool.Close()
	for name, q := range map[string]req{
		"staff bearer":     {method: "GET", url: e.public.URL + "/api/v1/reports", headers: map[string]string{"Authorization": "Bearer " + e.staff}},
		"staff session":    {method: "GET", url: e.public.URL + "/api/v1/reports", headers: asSession(session)},
		"who am I":         {method: "GET", url: e.public.URL + "/api/v1/staff/session", headers: asSession(session)},
		"candidate cookie": {method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: candidate},
	} {
		if r := e.do(q); r.status != 500 || r.str("code") != "internal_error" {
			t.Errorf("%s during an outage: %d %s", name, r.status, r.raw)
		}
	}
	if r := e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports"}); r.status != 401 {
		t.Fatalf("no credential at all is still 401: %d", r.status)
	}
}

// The OpenAPI document and the code agree on which operations accept the review console's cookie
// (ADR-023): accepted exactly where an operation's security lists staffSession.
func TestOnlyOperationsDeclaringStaffSessionAcceptTheCookie(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `yaml:"security"`
		} `yaml:"paths"`
	}
	readOpenAPI(t, &spec)
	e := newEnv(t)
	admin := e.staffToken("admin") // every role check passes, so only the credential decides
	fakeIDs := strings.NewReplacer("{interview_id}", ids.New(ids.Session), "{task_id}", ids.New(ids.ReviewTask))
	counts := map[string]int{}
	for _, rt := range api.PublicRoutes() {
		route := rt.Method + " " + rt.Pattern
		op, ok := spec.Paths[rt.Pattern][strings.ToLower(rt.Method)]
		if !ok {
			t.Fatalf("%s is not in the OpenAPI document", route)
		}
		if len(op.Security) == 0 { // security: [] (anonymous)
			counts["skipped"]++
			continue
		}
		declared := slices.ContainsFunc(op.Security, func(s map[string][]string) bool { _, ok := s["staffSession"]; return ok })
		url := e.public.URL + fakeIDs.Replace(rt.Pattern)
		headers := map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-origin"}
		if r := e.do(req{method: rt.Method, url: url, headers: headers}); r.status != 401 {
			t.Errorf("%s without a credential: %d (authentication must come first)", route, r.status)
			continue
		}
		// A fresh session per route: sign-out revokes the one it is given, and HTTP sign-ins are rate-limited.
		sess, err := e.svc.StartStaffSession(context.Background(), interview.Actor{}, ids.Hash(admin), "", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		headers["Cookie"] = api.StaffCookie + "=" + sess.Token
		r := e.do(req{method: rt.Method, url: url, headers: headers})
		if r.str("code") == "origin_mismatch" || r.str("detail") == "no such endpoint" || r.status == 429 || r.status >= 500 {
			t.Errorf("%s: %d %s says nothing about the cookie", route, r.status, r.raw)
			continue
		}
		accepted := r.status != 401
		if accepted != declared {
			t.Errorf("%s: cookie accepted=%v, but the OpenAPI document lists staffSession=%v", route, accepted, declared)
		}
		counts[map[bool]string{true: "accepted", false: "refused"}[accepted]]++
	}
	if counts["accepted"] != 7 || counts["refused"] != 14 || counts["skipped"] != 2 {
		t.Fatalf("operations: %v", counts)
	}
}
