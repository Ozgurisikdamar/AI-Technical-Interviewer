package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

var (
	shownNumber = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)
	shownCode   = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}-[0-9A-HJKMNP-TV-Z]{5}$`)
)

// candidateSignIn posts an application number and access code as the candidate page does (same
// origin).
func (e *env) candidateSignIn(number, code string, headers map[string]string) response {
	e.t.Helper()
	h := map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-origin"}
	for k, v := range headers {
		h[k] = v
	}
	return e.do(req{method: "POST", url: e.public.URL + "/api/v1/candidate/session", headers: h,
		body: map[string]any{"application_number": number, "access_code": code}})
}

// issued creates an interview over the API and returns what the hiring team receives for the
// candidate: the interview, the application number and the access code, both as shown.
func (e *env) issued() (id, number, code string) {
	e.t.Helper()
	r := e.createInterview(e.staff, "", ids.New("ref"))
	if r.status != 201 {
		e.t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id, number, code = r.str("interview", "id"), r.str("interview", "application_number"), r.str("access_code")
	if !shownNumber.MatchString(number) || !shownCode.MatchString(code) {
		e.t.Fatalf("issued %q and %q", number, code)
	}
	return id, number, code
}

// withoutRequestID is a problem body without the per-request field, for comparing refusals.
func withoutRequestID(t *testing.T, r response) string {
	t.Helper()
	body := map[string]any{}
	for k, v := range r.body {
		body[k] = v
	}
	delete(body, "request_id")
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestCandidateSignInOpensTheInterviewWithTheSessionCookie(t *testing.T) {
	e := newEnv(t)
	id, number, code := e.issued()
	// As a person might type them: lower case, without the hyphen, O for 0 and I or L for 1.
	typed := strings.NewReplacer("0", "o", "1", "l").Replace(strings.ToLower(strings.ReplaceAll(code, "-", "")))
	r := e.candidateSignIn(" "+strings.ToLower(number)+" ", typed, nil)
	if r.status != 200 || r.str("interview_id") != id || r.str("state") != "CONSENT_PENDING" {
		t.Fatalf("sign-in: %d %s", r.status, r.raw)
	}
	if strings.Contains(string(r.raw), code) || strings.Contains(string(r.raw), number) {
		t.Fatalf("the response repeats a credential: %s", r.raw)
	}
	cookie := candidateCookie(t, r)
	// The same cookie as the invitation link sets.
	other := e.createInterview(e.staff, "", ids.New("ref"))
	ex := e.do(req{method: "POST", url: e.public.URL + "/api/v1/invitations/exchange", headers: map[string]string{"Origin": origin},
		body: map[string]any{"token": strings.TrimPrefix(other.str("invitation_url"), origin+"/i#token=")}})
	attributes := func(r response) string {
		for _, c := range (&http.Response{Header: r.header}).Cookies() {
			if c.Name == api.CandidateCookie {
				c.Value, c.Raw = "", ""
				return c.String()
			}
		}
		return ""
	}
	if got, want := attributes(r), attributes(ex); got != want || want == "" || strings.Contains(got, "Secure") {
		t.Fatalf("cookie attributes %q, want those of the invitation link %q", got, want)
	}
	// The cookie opens the matched interview, which names the application.
	view := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, cookie: cookie})
	if view.status != 200 || view.str("application_number") != number || view.str("interview_title") == "" || view.str("language") != "en" {
		t.Fatalf("candidate view: %d %s", view.status, view.raw)
	}
	if strings.Contains(string(view.raw), code) {
		t.Fatal("the candidate view must not repeat the access code")
	}
	if n := e.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'candidate.sign_in' AND subject_id = $1 AND outcome = 'allowed'`, id); n != 1 {
		t.Fatalf("sign-in audits: %d", n)
	}
}

func TestCandidateSignInRefusesEveryBadAttemptAlike(t *testing.T) {
	e := newEnvWith(t, map[string]string{"ATI_CANDIDATE_SIGNINS_PER_MINUTE": "100"})
	_, number, code := e.issued()
	expiredID, expiredNumber, expiredCode := e.issued()
	revokedID, revokedNumber, revokedCode := e.issued()
	if r := e.revoke(revokedID, e.staff); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, r.raw)
	}
	if _, err := e.db.Owner.Exec(context.Background(), `UPDATE ati.invitations SET expires_at = now() - interval '1 second' WHERE session_id = $1`, expiredID); err != nil {
		t.Fatal(err)
	}
	var first string
	for name, attempt := range map[string][2]string{
		"unknown number":     {accesscode.FormatNumber(accesscode.NewNumber()), code},
		"wrong code":         {number, accesscode.FormatCode(accesscode.NewCode())},
		"swapped":            {code, number},
		"expired invitation": {expiredNumber, expiredCode},
		"revoked invitation": {revokedNumber, revokedCode},
		"malformed number":   {"7K3Q-9XW", code},
		"refused letter":     {number, strings.Replace(code, code[:1], "U", 1)},
		"empty":              {"", ""},
		"too long":           {number + strings.Repeat(" ", 40), code},
	} {
		r := e.candidateSignIn(attempt[0], attempt[1], nil)
		if r.status != 401 || r.str("code") != "sign_in_failed" || r.str("detail") != "the application number or access code was not accepted" {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
		// The same body for every reason: nothing tells an unknown number from a wrong code.
		if body := withoutRequestID(t, r); first == "" {
			first = body
		} else if body != first {
			t.Errorf("%s: %s differs from %s", name, body, first)
		}
		if len((&http.Response{Header: r.header}).Cookies()) != 0 {
			t.Errorf("%s: a refused sign-in sets no cookie", name)
		}
	}
	if n := e.count(`SELECT count(*) FROM ati.candidate_sessions`); n != 0 {
		t.Fatalf("refused sign-ins opened %d sessions", n)
	}
	// Login CSRF: another site, or no Origin at all, is refused before the code is looked at.
	for name, headers := range map[string]map[string]string{
		"cross-origin": {"Origin": "https://evil.example"},
		"cross-site":   {"Sec-Fetch-Site": "cross-site"},
		"no origin":    {"Origin": "", "Sec-Fetch-Site": ""},
	} {
		if r := e.candidateSignIn(number, code, headers); r.status != 403 || r.str("code") != "origin_mismatch" {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	url := e.public.URL + "/api/v1/candidate/session"
	same := func(extra map[string]string) map[string]string {
		h := map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-origin"}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	for name, c := range map[string]struct {
		q          req
		status     int
		code       string
		mentionsIt bool
	}{
		"without an Idempotency-Key": {req{method: "POST", url: url, headers: same(map[string]string{"Idempotency-Key": "x"}), body: map[string]any{"application_number": number, "access_code": code}}, 400, "idempotency_key_required", false},
		"an unknown field":           {req{method: "POST", url: url, headers: same(nil), body: map[string]any{"application_number": number, "access_code": code, "remember": true}}, 400, "invalid_request", false},
		"not JSON":                   {req{method: "POST", url: url, headers: same(map[string]string{"Content-Type": "text/plain"}), body: map[string]any{"application_number": number, "access_code": code}}, 415, "unsupported_media_type", false},
		"too large":                  {req{method: "POST", url: url, headers: same(nil), body: map[string]any{"application_number": number, "access_code": strings.Repeat("A", 20<<10)}}, 413, "payload_too_large", false},
	} {
		r := e.do(c.q)
		if r.status != c.status || r.str("code") != c.code {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
		if strings.Contains(string(r.raw), code) || strings.Contains(string(r.raw), number) {
			t.Errorf("%s: the problem repeats a credential: %s", name, r.raw)
		}
	}
	// None of this locked the right code out: one wrong code was counted, and five lock.
	if r := e.candidateSignIn(number, code, nil); r.status != 200 {
		t.Fatalf("the right code: %d %s", r.status, r.raw)
	}
}

func TestCandidateSignInIsRateLimitedPerClient(t *testing.T) {
	e := newEnvWith(t, map[string]string{"ATI_CANDIDATE_SIGNINS_PER_MINUTE": "3"})
	_, number, code := e.issued()
	for i := range 2 {
		if r := e.candidateSignIn(number, accesscode.FormatCode(accesscode.NewCode()), nil); r.status != 401 {
			t.Fatalf("attempt %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := e.candidateSignIn(number, code, nil); r.status != 200 {
		t.Fatalf("third attempt: %d %s", r.status, r.raw)
	}
	r := e.candidateSignIn(number, code, nil)
	if r.status != 429 || r.str("code") != "rate_limited" || len((&http.Response{Header: r.header}).Cookies()) != 0 {
		t.Fatalf("fourth attempt: %d %s", r.status, r.raw)
	}
	// The budget is separate from the invitation exchange's and the staff sign-in's.
	if r := e.signIn(e.staff, nil); r.status != 200 {
		t.Fatalf("staff sign-in: %d %s", r.status, r.raw)
	}
}

// The number belongs to the hiring team and the candidate: a recruiter sees it on the interview,
// a reviewer never does, so it cannot link an answer to a person (ADR-022, ADR-024).
func TestTheApplicationNumberIsShownToRecruitersButNeverInReview(t *testing.T) {
	e := newEnv(t)
	id, _ := e.inReview()
	var canonical string
	if err := e.db.Owner.QueryRow(context.Background(), `SELECT application_number FROM ati.interview_sessions WHERE id = $1`, id).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	shown := accesscode.FormatNumber(canonical)
	staffView := e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id, headers: map[string]string{"Authorization": "Bearer " + e.staff}})
	if staffView.status != 200 || staffView.str("application_number") != shown {
		t.Fatalf("staff view: %d %s", staffView.status, staffView.raw)
	}
	reviewer := map[string]string{"Authorization": "Bearer " + e.staffToken("reviewer")}
	queue := e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks?interview_id=" + id, headers: reviewer})
	if queue.status != 200 {
		t.Fatalf("queue: %d %s", queue.status, queue.raw)
	}
	seen := []response{queue}
	tasks, _ := queue.body["review_tasks"].([]any)
	if len(tasks) == 0 {
		t.Fatal("no review tasks")
	}
	// Each task as the reviewer reads it, then decided, so the report exists.
	for _, task := range tasks {
		taskID, _ := task.(map[string]any)["task_id"].(string)
		seen = append(seen, e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks/" + taskID, headers: reviewer}))
		if r := e.do(req{method: "POST", url: e.public.URL + "/api/v1/review-tasks/" + taskID + "/outcome", headers: reviewer, body: map[string]any{
			"scores":    map[string]int{"technical_accuracy": 60, "architecture_maturity": 50, "edge_case_awareness": 40, "claim_support": 30},
			"rationale": "Accurate but brief; no example."}}); r.status != 201 {
			t.Fatalf("decision: %d %s", r.status, r.raw)
		}
	}
	seen = append(seen,
		e.do(req{method: "GET", url: e.public.URL + "/api/v1/review-tasks?status=completed&interview_id=" + id, headers: reviewer}),
		e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/report", headers: reviewer}),
		e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports", headers: reviewer}),
		e.do(req{method: "GET", url: e.public.URL + "/api/v1/interviews/" + id + "/report", headers: map[string]string{"Authorization": "Bearer " + e.staff}}),
		e.do(req{method: "GET", url: e.public.URL + "/api/v1/reports", headers: map[string]string{"Authorization": "Bearer " + e.staff}}),
	)
	for i, r := range seen {
		if r.status != 200 {
			t.Fatalf("read %d: %d %s", i, r.status, r.raw)
		}
		if strings.Contains(string(r.raw), canonical) || strings.Contains(string(r.raw), shown) || strings.Contains(string(r.raw), "application_number") {
			t.Fatalf("read %d shows the application number: %s", i, r.raw)
		}
	}
}
