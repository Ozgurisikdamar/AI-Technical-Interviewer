package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
)

// CandidateCookie carries the opaque candidate session token (HttpOnly, SameSite=Strict).
const CandidateCookie = "ati_cs"

// StaffTokenPrefix distinguishes staff bearer tokens from the agent service credential.
const StaffTokenPrefix = "ati_st_"

// StaffCookie carries the opaque staff browser session token (HttpOnly, SameSite=Strict, ADR-023).
const StaffCookie = "ati_ss"

// staffCredential is the set of staff credentials an endpoint accepts.
type staffCredential bool

const (
	// bearerOnly: interview management, and every endpoint a candidate's browser could reach.
	// A staff member's browser may also hold a candidate cookie (ADR-023).
	bearerOnly staffCredential = false
	// bearerOrSession: review, reports and the staff session itself.
	bearerOrSession staffCredential = true
)

type principalKind int

const (
	kindNone principalKind = iota
	kindStaff
	kindCandidate
	kindAgent
)

type principal struct {
	kind      principalKind
	staff     *interview.StaffPrincipal
	sessionID string // candidate: the one interview the cookie is bound to
	lease     *interview.LeaseRef
}

func (p principal) idemKey() string {
	switch p.kind {
	case kindStaff:
		return "staff:" + p.staff.TokenID
	case kindCandidate:
		return "cand:" + p.sessionID
	}
	return ""
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}

// errNoCredential means the request carries no credential the endpoint accepts, or a malformed one.
var errNoCredential = errors.New("no credential")

// authStaff authenticates a staff member. An Authorization header alone decides when one is
// present; otherwise the browser session cookie counts where the endpoint accepts it. It fails with
// errNoCredential or interview.ErrNotFound when the request is not authenticated, and with any
// other error when authentication itself failed.
func (s *Server) authStaff(r *http.Request, accepts staffCredential) (*interview.StaffPrincipal, error) {
	if r.Header.Get("Authorization") != "" {
		tok := bearer(r)
		if !strings.HasPrefix(tok, StaffTokenPrefix) {
			return nil, errNoCredential
		}
		return s.svc.AuthenticateStaff(r.Context(), ids.Hash(tok))
	}
	if accepts != bearerOrSession {
		return nil, errNoCredential
	}
	tok, ok := cookieValue(r, StaffCookie)
	if !ok {
		return nil, errNoCredential
	}
	return s.svc.AuthenticateStaffSession(r.Context(), ids.Hash(tok))
}

// authCandidate authenticates the candidate cookie, failing like authStaff.
func (s *Server) authCandidate(r *http.Request) (string, error) {
	tok, ok := cookieValue(r, CandidateCookie)
	if !ok {
		return "", errNoCredential
	}
	return s.svc.AuthenticateCandidate(r.Context(), tok)
}

// authFailed answers a request that did not authenticate: 401 when it carried no valid credential,
// and the logged 500 of writeError when authentication itself failed (for example the database),
// so an outage is never mistaken for a signed-out user.
func (s *Server) authFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNoCredential) || errors.Is(err, interview.ErrNotFound) {
		unauthorized(w, r)
		return
	}
	s.writeError(w, r, err)
}

// cookieValue returns an opaque session cookie (candidate or staff) when it is well formed.
func cookieValue(r *http.Request, name string) (string, bool) {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 128 {
		return "", false
	}
	return c.Value, true
}

// apiCookie builds a session cookie of the public API: HttpOnly, SameSite=Strict, scoped to /api
// and Secure outside dev and test (ADR-016, ADR-023). A negative maxAge deletes it.
func (s *Server) apiCookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/api", HttpOnly: true, Secure: s.cfg.SecureCookies(),
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}

// staffPrincipal authenticates a staff member of any role.
func (s *Server) staffPrincipal(w http.ResponseWriter, r *http.Request, accepts staffCredential) (principal, bool) {
	staff, err := s.authStaff(r, accepts)
	if err != nil {
		s.authFailed(w, r, err)
		return principal{}, false
	}
	return principal{kind: kindStaff, staff: staff}, true
}

// staffWith authenticates a staff member holding one of the roles.
func (s *Server) staffWith(w http.ResponseWriter, r *http.Request, accepts staffCredential, roles ...string) (principal, bool) {
	p, ok := s.staffPrincipal(w, r, accepts)
	if ok && !slices.Contains(roles, p.staff.Role) {
		httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "role not permitted")
		return principal{}, false
	}
	return p, ok
}

// authAgentService checks the Tier-1 service credential in constant time.
func (s *Server) authAgentService(r *http.Request) bool {
	tok := bearer(r)
	return tok != "" && ids.Equal(ids.Hash(tok), s.cfg.AgentServiceTokenSHA256)
}

// authAgentLease resolves the per-session grant and fencing token (re-checked in every transaction).
func (s *Server) authAgentLease(r *http.Request, sessionID string) (*interview.LeaseRef, bool) {
	if !s.authAgentService(r) {
		return nil, false
	}
	grant := r.Header.Get("X-ATI-Agent-Grant")
	fencing, err := strconv.ParseInt(r.Header.Get("X-ATI-Fencing-Token"), 10, 64)
	if grant == "" || err != nil || fencing < 1 {
		return nil, false
	}
	ref, err := s.svc.AuthenticateLease(r.Context(), sessionID, grant, fencing)
	return ref, err == nil
}

func actorFor(ctx context.Context, p principal) interview.Actor {
	a := interview.Actor{RequestID: httpx.RequestIDFrom(ctx)}
	switch p.kind {
	case kindCandidate:
		a.Type, a.ID = flow.ActorCandidate, p.sessionID
	case kindAgent:
		a.Type = flow.ActorTier1
		if p.lease != nil {
			a.ID = p.lease.LeaseID
		}
	case kindStaff:
		a.Type, a.ID, a.StaffSession = flow.ActorSystem, p.staff.TokenID, p.staff.SessionID
	}
	return a
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
}
