package api

import (
	"context"
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

// authStaff authenticates a staff member. An Authorization header alone decides when one is
// present; otherwise the browser session cookie counts where the endpoint accepts it.
func (s *Server) authStaff(r *http.Request, accepts staffCredential) (*interview.StaffPrincipal, bool) {
	if r.Header.Get("Authorization") != "" {
		tok := bearer(r)
		if !strings.HasPrefix(tok, StaffTokenPrefix) {
			return nil, false
		}
		p, err := s.svc.AuthenticateStaff(r.Context(), ids.Hash(tok))
		return p, err == nil
	}
	if accepts != bearerOrSession {
		return nil, false
	}
	tok, ok := staffSessionCookie(r)
	if !ok {
		return nil, false
	}
	p, err := s.svc.AuthenticateStaffSession(r.Context(), ids.Hash(tok))
	return p, err == nil
}

// staffSessionCookie returns the staff session cookie when it is well formed.
func staffSessionCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(StaffCookie)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 128 {
		return "", false
	}
	return c.Value, true
}

// staffWith authenticates a staff member holding one of the roles.
func (s *Server) staffWith(w http.ResponseWriter, r *http.Request, accepts staffCredential, roles ...string) (principal, bool) {
	staff, ok := s.authStaff(r, accepts)
	if !ok {
		unauthorized(w, r)
		return principal{}, false
	}
	if !slices.Contains(roles, staff.Role) {
		httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "role not permitted")
		return principal{}, false
	}
	return principal{kind: kindStaff, staff: staff}, true
}

// authCandidate authenticates the candidate cookie.
func (s *Server) authCandidate(r *http.Request) (string, bool) {
	c, err := r.Cookie(CandidateCookie)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 128 {
		return "", false
	}
	id, err := s.svc.AuthenticateCandidate(r.Context(), c.Value)
	return id, err == nil
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
		a.Type, a.ID = flow.ActorSystem, p.staff.TokenID
	}
	return a
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
}
