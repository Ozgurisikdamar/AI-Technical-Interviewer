package api

// Staff browser sessions (ADR-023). The review console signs a staff member in by exchanging a
// pasted staff token for a short-lived HttpOnly session cookie, so no credential is kept in
// JavaScript-readable storage. The cookie is accepted only by the review, report and session
// endpoints; interview management keeps requiring the bearer token.

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
)

// staffTokenFormat is the shape of a staff token; anything else is refused before any lookup.
var staffTokenFormat = regexp.MustCompile(`^` + StaffTokenPrefix + `[A-Za-z0-9_-]{20,120}$`)

type staffSessionView struct {
	Role      string    `json:"role"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expires_at"`
}

func sessionView(p *interview.StaffPrincipal) staffSessionView {
	return staffSessionView{Role: p.Role, Label: p.Label, ExpiresAt: p.ExpiresAt.UTC()}
}

// startStaffSession signs a staff member in. It is not replayed from storage: each execution
// yields a fresh session and revokes the one the browser still presents, so a retry is safe and
// no session token is ever stored (as with the invitation exchange). The request carries no
// bearer token, so the same-origin guard applies to it, which also refuses login CSRF.
func (s *Server) startStaffSession(w http.ResponseWriter, r *http.Request) {
	if !s.staffLimiter.allow(httpx.ClientIP(r, s.cfg.TrustedProxies), s.now()) {
		httpx.WriteProblem(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts; wait and try again")
		return
	}
	if !requireIdempotencyKey(w, r) {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &req, maxPublicBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	// Malformed, unknown, expired and revoked tokens are refused alike.
	if !staffTokenFormat.MatchString(req.Token) {
		staffSignInRefused(w, r)
		return
	}
	previous := ""
	if tok, ok := cookieValue(r, StaffCookie); ok {
		previous = ids.Hash(tok)
	}
	sess, err := s.svc.StartStaffSession(r.Context(), interview.Actor{RequestID: httpx.RequestIDFrom(r.Context())}, ids.Hash(req.Token), previous, s.cfg.StaffSessionTTL)
	if err != nil {
		if errors.Is(err, interview.ErrNotFound) {
			staffSignInRefused(w, r)
			return
		}
		s.writeError(w, r, err)
		return
	}
	// The service refuses sessions shorter than MinStaffSession, so this is about a minute or more.
	http.SetCookie(w, s.apiCookie(StaffCookie, sess.Token, int(sess.Principal.ExpiresAt.Sub(s.now())/time.Second)))
	httpx.WriteJSON(w, http.StatusOK, sessionView(&sess.Principal))
}

func staffSignInRefused(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "the staff token is invalid, expired or revoked")
}

// getStaffSession tells the console who is signed in, with which role and until when.
func (s *Server) getStaffSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffPrincipal(w, r, bearerOrSession)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sessionView(p.staff))
}

// endStaffSession signs out: the session is revoked and its cookie cleared. A bearer token has no
// session to end, so nothing is cleared then: a cookie sent alongside stays valid, and clearing it
// would only pretend otherwise. The answer is JSON because the console parses every answer.
func (s *Server) endStaffSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffPrincipal(w, r, bearerOrSession)
	if !ok || !requireIdempotencyKey(w, r) {
		return
	}
	ended, err := s.svc.EndStaffSession(r.Context(), actorFor(r.Context(), p), p.staff)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if ended {
		http.SetCookie(w, s.apiCookie(StaffCookie, "", -1))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ended": ended})
}
