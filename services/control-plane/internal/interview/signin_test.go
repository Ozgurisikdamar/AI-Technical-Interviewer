package interview

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

func (h *harness) create(plan string) *Created {
	h.t.Helper()
	c, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "stf_test", CandidateRef: ids.New("ref"), PlanID: plan, PlanVersion: 1})
	h.must(err)
	return c
}

// countVerifications counts the argon2 verifications the service performs from now on.
func (h *harness) countVerifications() *atomic.Int64 {
	var n atomic.Int64
	h.svc.verify = func(code, phc string) bool {
		n.Add(1)
		return accesscode.Verify(code, phc)
	}
	return &n
}

func TestCreatingAnInterviewIssuesANumberAndACodeStoredOnlyAsAHash(t *testing.T) {
	h := newHarness(t)
	c := h.create("plan_demo_backend_en")
	if _, ok := accesscode.Normalize(c.ApplicationNumber, accesscode.NumberLength); !ok || len(c.ApplicationNumber) != 8 {
		t.Fatalf("application number %q", c.ApplicationNumber)
	}
	if _, ok := accesscode.Normalize(c.AccessCode, accesscode.CodeLength); !ok || len(c.AccessCode) != 10 {
		t.Fatalf("access code %q", c.AccessCode)
	}
	var number, hash string
	h.must(h.db.Owner.QueryRow(ctx, `SELECT s.application_number, i.access_code_hash FROM ati.interview_sessions s JOIN ati.invitations i ON i.session_id = s.id WHERE s.id = $1`,
		c.SessionID).Scan(&number, &hash))
	if number != c.ApplicationNumber || !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(hash, c.AccessCode) || !accesscode.Verify(c.AccessCode, hash) {
		t.Fatalf("stored %q %q", number, hash)
	}
	if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE details::text LIKE $1`, "%"+c.AccessCode+"%"); n != 0 {
		t.Fatal("the access code reached the audit log")
	}
	view, err := h.svc.StaffView(ctx, c.SessionID, h.tenant)
	h.must(err)
	if view.ApplicationNumber == nil || *view.ApplicationNumber != accesscode.FormatNumber(c.ApplicationNumber) {
		t.Fatalf("staff view number: %v", view.ApplicationNumber)
	}
}

func TestSignInOpensASessionLikeAnInvitationExchange(t *testing.T) {
	h := newHarness(t)
	c := h.create("plan_demo_backend_en")
	// As a person might type them: lower case, a space instead of the hyphen, O for 0 if present.
	number := strings.ToLower(accesscode.FormatNumber(c.ApplicationNumber))
	code := strings.ReplaceAll(strings.ToLower(accesscode.FormatCode(c.AccessCode)), "-", " ")
	ex, err := h.svc.SignInCandidate(ctx, candidate, " "+number+" ", code)
	h.must(err)
	if ex.SessionID != c.SessionID || ex.State != flow.ConsentPending {
		t.Fatalf("signed in to %s in %s", ex.SessionID, ex.State)
	}
	if got, err := h.svc.AuthenticateCandidate(ctx, ex.CandidateToken); err != nil || got != c.SessionID {
		t.Fatalf("session: %v %v", got, err)
	}
	if n := h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1 AND transition_id = 'T01'`, c.SessionID); n != 1 {
		t.Fatalf("T01 performed %d times", n)
	}
	for action, want := range map[string]int{"candidate.sign_in": 1, "invitation.exchange": 0} {
		if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE action = $1 AND subject_id = $2`, action, c.SessionID); n != want {
			t.Errorf("%s audited %d times, want %d", action, n, want)
		}
	}
	// A second sign-in works too (another device, a lost cookie) and does not repeat T01.
	again, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
	h.must(err)
	if again.State != flow.ConsentPending || h.count(`SELECT count(*) FROM ati.session_events WHERE session_id = $1 AND transition_id = 'T01'`, c.SessionID) != 1 {
		t.Fatal("a second sign-in must not repeat T01")
	}
}

func TestSignInAndTheInvitationLinkEndEachOthersSessions(t *testing.T) {
	h := newHarness(t)
	c := h.create("plan_demo_backend_en")
	signedIn, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
	h.must(err)
	exchanged, err := h.svc.ExchangeInvitation(ctx, candidate, c.InvitationToken)
	h.must(err)
	if _, err := h.svc.AuthenticateCandidate(ctx, signedIn.CandidateToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("the link must end the session the sign-in opened")
	}
	again, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
	h.must(err)
	if _, err := h.svc.AuthenticateCandidate(ctx, exchanged.CandidateToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("a sign-in must end the session the link opened")
	}
	if _, err := h.svc.AuthenticateCandidate(ctx, again.CandidateToken); err != nil {
		t.Fatal(err)
	}
}

func TestEveryRefusedSignInLooksTheSame(t *testing.T) {
	h := newHarness(t)
	verifications := h.countVerifications()
	unknown := h.create("plan_demo_backend_en")
	expired := h.create("plan_demo_backend_en")
	revoked := h.create("plan_demo_backend_en")
	older := h.create("plan_demo_backend_en")
	_, err := h.svc.RevokeInvitation(ctx, staff, revoked.SessionID, h.tenant)
	h.must(err)
	_, err = h.db.Owner.Exec(ctx, `UPDATE ati.interview_sessions SET application_number = NULL WHERE id = $1`, older.SessionID)
	h.must(err)
	_, err = h.db.Owner.Exec(ctx, `UPDATE ati.invitations SET expires_at = $2 WHERE session_id = $1`, expired.SessionID, h.clock.Now().Add(-time.Minute))
	h.must(err)
	notIssued := accesscode.NewNumber()
	for name, attempt := range map[string][2]string{
		"unknown number":        {notIssued, unknown.AccessCode},
		"wrong code":            {unknown.ApplicationNumber, accesscode.NewCode()},
		"expired invitation":    {expired.ApplicationNumber, expired.AccessCode},
		"revoked invitation":    {revoked.ApplicationNumber, revoked.AccessCode},
		"interview without one": {older.ApplicationNumber, older.AccessCode},
	} {
		before := verifications.Load()
		if _, err := h.svc.SignInCandidate(ctx, candidate, attempt[0], attempt[1]); !errors.Is(err, ErrSignInFailed) {
			t.Errorf("%s: %v", name, err)
		}
		// One verification on every path, so the work done does not tell the reasons apart.
		if n := verifications.Load() - before; n != 1 {
			t.Errorf("%s: %d verifications", name, n)
		}
	}
	for name, attempt := range map[string][2]string{
		"malformed number": {"7K3Q-9XW", unknown.AccessCode},
		"malformed code":   {unknown.ApplicationNumber, "ABCDE-FGHJU"},
		"empty":            {"", ""},
	} {
		before := verifications.Load()
		if _, err := h.svc.SignInCandidate(ctx, candidate, attempt[0], attempt[1]); !errors.Is(err, ErrSignInFailed) {
			t.Errorf("%s: %v", name, err)
		}
		if verifications.Load() != before {
			t.Errorf("%s: a malformed attempt was verified", name)
		}
	}
	if n := h.count(`SELECT count(*) FROM ati.candidate_sessions`); n != 0 {
		t.Fatalf("a refused sign-in opened %d sessions", n)
	}
}

func TestFiveWrongCodesLockCodeSignInForAWhile(t *testing.T) {
	h := newHarness(t)
	// Four wrong codes do not lock: the right one still works, and resets the count.
	c := h.create("plan_demo_backend_en")
	for range SignInMaxFailures - 1 {
		if _, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, accesscode.NewCode()); !errors.Is(err, ErrSignInFailed) {
			t.Fatal(err)
		}
	}
	_, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
	h.must(err)
	if n := h.count(`SELECT access_failures FROM ati.invitations WHERE session_id = $1`, c.SessionID); n != 0 {
		t.Fatalf("failures after a success: %d", n)
	}

	// The fifth wrong code in a row locks: even the right code is refused, and the lock is audited once.
	d := h.create("plan_demo_backend_en")
	for range SignInMaxFailures + 2 {
		if _, err := h.svc.SignInCandidate(ctx, candidate, d.ApplicationNumber, accesscode.NewCode()); !errors.Is(err, ErrSignInFailed) {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.SignInCandidate(ctx, candidate, d.ApplicationNumber, d.AccessCode); !errors.Is(err, ErrSignInFailed) {
		t.Fatalf("the right code during a lock: %v", err)
	}
	if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'candidate.sign_in.locked' AND subject_id = $1 AND outcome = 'denied'`, d.SessionID); n != 1 {
		t.Fatalf("lock audited %d times", n)
	}
	// The invitation link is not affected by the lock.
	if _, err := h.svc.ExchangeInvitation(ctx, candidate, d.InvitationToken); err != nil {
		t.Fatalf("the link during a lock: %v", err)
	}
	// Attempts during the lock do not extend it.
	h.clock.Advance(SignInLockDuration - time.Minute)
	if _, err := h.svc.SignInCandidate(ctx, candidate, d.ApplicationNumber, d.AccessCode); !errors.Is(err, ErrSignInFailed) {
		t.Fatal("the lock ended early")
	}
	h.clock.Advance(time.Minute)
	if _, err := h.svc.SignInCandidate(ctx, candidate, d.ApplicationNumber, d.AccessCode); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
}

// The attempt is counted before the code is verified, so parallel guesses cannot all slip in
// before the lock: exactly one lock engages, and it holds.
func TestParallelGuessesCannotExceedTheLimit(t *testing.T) {
	h := newHarness(t)
	c := h.create("plan_demo_backend_en")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, accesscode.NewCode()); !errors.Is(err, ErrSignInFailed) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := h.count(`SELECT count(*) FROM ati.audit_logs WHERE action = 'candidate.sign_in.locked' AND subject_id = $1`, c.SessionID); n != 1 {
		t.Fatalf("%d locks engaged", n)
	}
	if _, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode); !errors.Is(err, ErrSignInFailed) {
		t.Fatal("the lock did not hold")
	}
}

func TestBusySignInsAreRefusedWithoutCountingAnAttempt(t *testing.T) {
	h := newHarness(t)
	c := h.create("plan_demo_backend_en")
	h.svc.verifierWait = 20 * time.Millisecond
	for range signInVerifiers {
		h.svc.verifiers <- struct{}{}
	}
	if _, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode); !errors.Is(err, ErrSignInBusy) {
		t.Fatalf("busy: %v", err)
	}
	for range signInVerifiers {
		<-h.svc.verifiers
	}
	if n := h.count(`SELECT access_failures FROM ati.invitations WHERE session_id = $1`, c.SessionID); n != 0 {
		t.Fatalf("a refused busy attempt was counted: %d", n)
	}
	_, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
	h.must(err)
}

// A sign-in racing a revocation never deadlocks, and no session survives the revocation.
func TestSignInAndRevocationDoNotDeadlock(t *testing.T) {
	h := newHarness(t)
	for i := range 25 {
		c := h.create("plan_demo_backend_en")
		var wg sync.WaitGroup
		var signErr, revErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, signErr = h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
		}()
		go func() { defer wg.Done(); _, revErr = h.svc.RevokeInvitation(ctx, staff, c.SessionID, h.tenant) }()
		wg.Wait()
		if revErr != nil || (signErr != nil && !errors.Is(signErr, ErrSignInFailed)) {
			t.Fatalf("iteration %d: sign-in=%v revoke=%v", i, signErr, revErr)
		}
		if n := h.count(`SELECT count(*) FROM ati.candidate_sessions WHERE session_id = $1 AND revoked_at IS NULL`, c.SessionID); n != 0 {
			t.Fatalf("iteration %d: a candidate session survived the revocation", i)
		}
	}
}

func TestApplicationNumberCollisionsAreRetried(t *testing.T) {
	h := newHarness(t)
	taken := h.create("plan_demo_backend_en").ApplicationNumber
	fresh := accesscode.NewNumber()
	calls := 0
	h.svc.newNumber = func() string {
		calls++
		if calls <= 2 {
			return taken
		}
		return fresh
	}
	if got := h.create("plan_demo_backend_en").ApplicationNumber; got != fresh || calls != 3 {
		t.Fatalf("after two collisions: %q in %d tries", got, calls)
	}
	h.svc.newNumber = func() string { return taken }
	_, err := h.svc.CreateInterview(ctx, staff, CreateRequest{TenantID: h.tenant, StaffID: "stf_test", CandidateRef: "ref-collide", PlanID: "plan_demo_backend_en", PlanVersion: 1})
	if !isUniqueViolation(err, applicationNumberKey) {
		t.Fatalf("three collisions in a row must fail: %v", err)
	}
}

func TestTheCandidateViewShowsTheMatchedApplication(t *testing.T) {
	h := newHarness(t)
	for plan, language := range map[string]string{"plan_demo_backend_en": "en", "plan_demo_backend_tr": "tr"} {
		c := h.create(plan)
		_, err := h.svc.SignInCandidate(ctx, candidate, c.ApplicationNumber, c.AccessCode)
		h.must(err)
		v, err := h.svc.CandidateView(ctx, c.SessionID, "test")
		h.must(err)
		if v.ApplicationNumber == nil || *v.ApplicationNumber != accesscode.FormatNumber(c.ApplicationNumber) || v.Language != language || v.InterviewTitle == "" {
			t.Fatalf("%s: %v %q %q", plan, v.ApplicationNumber, v.Language, v.InterviewTitle)
		}
		var title string
		h.must(h.db.Owner.QueryRow(ctx, `SELECT content->>'title' FROM ati.interview_plans WHERE plan_id = $1 AND version = 1`, plan).Scan(&title))
		if v.InterviewTitle != title {
			t.Fatalf("%s: title %q, want %q", plan, v.InterviewTitle, title)
		}
	}
	// An interview created before ADR-024 has no number.
	c := h.create("plan_demo_backend_en")
	_, err := h.db.Owner.Exec(ctx, `UPDATE ati.interview_sessions SET application_number = NULL WHERE id = $1`, c.SessionID)
	h.must(err)
	v, err := h.svc.CandidateView(ctx, c.SessionID, "test")
	h.must(err)
	if v.ApplicationNumber != nil {
		t.Fatalf("number of an older interview: %v", *v.ApplicationNumber)
	}
}
