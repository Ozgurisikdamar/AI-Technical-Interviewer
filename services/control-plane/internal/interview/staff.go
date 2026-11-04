package interview

// Staff authentication (ADR-016, ADR-023). Staff hold bearer tokens issued by ati-admin. In a
// browser, a staff member exchanges one for a short-lived session, kept in an HttpOnly cookie;
// the control plane stores only its hash. A session is valid only while its staff token is, so
// revoking the token ends every session made from it.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// StaffPrincipal is an authenticated staff member: a bearer token, or a browser session made from one.
type StaffPrincipal struct {
	// TokenID is the staff token's id. It identifies the staff member everywhere: as reviewer,
	// as the scope of idempotency keys and as the audited actor, whichever credential was used.
	TokenID  string
	TenantID string
	Role     string
	Label    string
	// ExpiresAt is when the credential that authenticated the request stops working.
	ExpiresAt time.Time
	// SessionID is the browser session; empty for a bearer token.
	SessionID string
}

// AuthenticateStaff resolves a staff bearer token (stored hashed, expiring, revocable).
func (s *Service) AuthenticateStaff(ctx context.Context, tokenHash string) (*StaffPrincipal, error) {
	return s.staffToken(ctx, s.pool, tokenHash, s.now().UTC())
}

func (s *Service) staffToken(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, tokenHash string, now time.Time) (*StaffPrincipal, error) {
	var p StaffPrincipal
	err := q.QueryRow(ctx, `SELECT id, tenant_id, role, label, expires_at FROM ati.staff_tokens WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2`,
		tokenHash, now).Scan(&p.TokenID, &p.TenantID, &p.Role, &p.Label, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// StaffSession is a new browser session. Token is the cookie value: returned once, stored only as a hash.
type StaffSession struct {
	Principal StaffPrincipal
	Token     string
}

// StartStaffSession exchanges a staff token for a browser session that ends after ttl, or with the
// token if that comes first. A session the browser still presents is revoked, so signing in again
// never leaves a second session behind. Failed sign-ins are not audited (as with invitations): the
// caller rate-limits them and they carry nothing worth keeping.
func (s *Service) StartStaffSession(ctx context.Context, actor Actor, tokenHash, previousSessionHash string, ttl time.Duration) (*StaffSession, error) {
	out := &StaffSession{Token: ids.Token()}
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		now := s.now().UTC()
		p, err := s.staffToken(ctx, tx, tokenHash, now)
		if err != nil {
			return err
		}
		if expires := now.Add(ttl); expires.Before(p.ExpiresAt) {
			p.ExpiresAt = expires
		}
		// Whole seconds: the cookie's Max-Age is in seconds, and the stored and reported times agree.
		if p.ExpiresAt = p.ExpiresAt.Truncate(time.Second); !p.ExpiresAt.After(now) {
			return ErrNotFound // the token expires within the second
		}
		p.SessionID = ids.New(ids.StaffSession)
		replaced := false
		if previousSessionHash != "" {
			tag, err := tx.Exec(ctx, `UPDATE ati.staff_sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, previousSessionHash, now)
			if err != nil {
				return err
			}
			replaced = tag.RowsAffected() > 0
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.staff_sessions (id, staff_token_id, token_hash, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
			p.SessionID, p.TokenID, ids.Hash(out.Token), now, p.ExpiresAt); err != nil {
			return err
		}
		out.Principal = *p
		return s.audit(ctx, tx, p.TenantID, staffActor(actor, p), "staff.session.start", "staff_session", p.SessionID, "allowed",
			map[string]any{"role": p.Role, "expires_at": p.ExpiresAt, "replaced_session": replaced})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AuthenticateStaffSession resolves a browser session cookie, checking the session and its staff token.
func (s *Service) AuthenticateStaffSession(ctx context.Context, sessionHash string) (*StaffPrincipal, error) {
	var p StaffPrincipal
	err := s.pool.QueryRow(ctx, `SELECT t.id, t.tenant_id, t.role, t.label, LEAST(s.expires_at, t.expires_at), s.id
		FROM ati.staff_sessions s JOIN ati.staff_tokens t ON t.id = s.staff_token_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND t.revoked_at IS NULL AND t.expires_at > $2`,
		sessionHash, s.now().UTC()).Scan(&p.TokenID, &p.TenantID, &p.Role, &p.Label, &p.ExpiresAt, &p.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// EndStaffSession revokes the principal's browser session (sign-out). It reports false when there
// was none to end: a bearer token, or a session that ended meanwhile.
func (s *Service) EndStaffSession(ctx context.Context, actor Actor, p *StaffPrincipal) (bool, error) {
	if p.SessionID == "" {
		return false, nil
	}
	ended := false
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		tag, err := tx.Exec(ctx, `UPDATE ati.staff_sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, p.SessionID, s.now().UTC())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		ended = true
		return s.audit(ctx, tx, p.TenantID, staffActor(actor, p), "staff.session.end", "staff_session", p.SessionID, "allowed", nil)
	})
	return ended, err
}

// staffActor is the audited actor of a staff request: the staff token, never the credential.
func staffActor(a Actor, p *StaffPrincipal) Actor {
	return Actor{Type: flow.ActorSystem, ID: p.TokenID, RequestID: a.RequestID}
}
