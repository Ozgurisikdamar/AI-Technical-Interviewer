package interview

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// Candidate sign-in limits (ADR-024).
const (
	// SignInMaxFailures wrong codes in a row lock code sign-in for SignInLockDuration. The lock does
	// not extend while it lasts, and the invitation link keeps working.
	SignInMaxFailures  = 5
	SignInLockDuration = 15 * time.Minute
	// signInVerifiers bounds concurrent argon2 verifications: each holds about 19 MiB for tens of
	// milliseconds. A sign-in waits at most signInWait for a slot, then gets ErrSignInBusy.
	signInVerifiers = 4
	signInWait      = 3 * time.Second
)

// SignInCandidate opens a candidate session from an application number and an access code
// (ADR-024).
//
// The code is a second credential of the interview's invitation, so the invitation's expiry and
// revocation apply to it, and a success behaves exactly like an invitation exchange.
//
// Every refusal is the same ErrSignInFailed: an unknown number, a wrong code, an expired or revoked
// invitation, and a lock alike. Past the format check (the format is public), every attempt
// verifies one argon2 hash, against a dummy one when the number is unknown, so the work done does
// not tell the reasons apart.
//
// An attempt is counted before its code is verified, so parallel guesses cannot exceed the limit.
func (s *Service) SignInCandidate(ctx context.Context, actor Actor, applicationNumber, accessCode string) (*Exchanged, error) {
	number, okNumber := accesscode.Normalize(applicationNumber, accesscode.NumberLength)
	code, okCode := accesscode.Normalize(accessCode, accesscode.CodeLength)
	if !okNumber || !okCode {
		return nil, ErrSignInFailed
	}
	release, err := s.acquireVerifier(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	var invitationID, sessionID, tenantID string
	err = s.pool.QueryRow(ctx, `SELECT i.id, s.id, s.tenant_id FROM ati.interview_sessions s JOIN ati.invitations i ON i.session_id = s.id
		WHERE s.application_number = $1 AND i.access_code_hash IS NOT NULL ORDER BY i.created_at DESC, i.id LIMIT 1`, number).
		Scan(&invitationID, &sessionID, &tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.verify(code, s.dummyHash())
		return nil, ErrSignInFailed
	}
	if err != nil {
		return nil, err
	}

	// Reserve the attempt: count it, and engage the lock when it is the last one allowed, unless
	// the invitation is revoked, expired or already locked. The counter starts again once a lock
	// engages, so the first failure after the lock does not lock again at once.
	now := s.now().UTC()
	var engaged bool
	var hash string
	err = s.pool.QueryRow(ctx, `UPDATE ati.invitations
		SET access_failures = CASE WHEN access_failures >= $4 - 1 THEN 0 ELSE access_failures + 1 END,
		    access_locked_until = CASE WHEN access_failures >= $4 - 1 THEN $3::timestamptz ELSE NULL END
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > $2 AND (access_locked_until IS NULL OR access_locked_until <= $2)
		RETURNING access_locked_until IS NOT NULL, access_code_hash`, invitationID, now, now.Add(SignInLockDuration), SignInMaxFailures).
		Scan(&engaged, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		var stored string
		if s.pool.QueryRow(ctx, `SELECT access_code_hash FROM ati.invitations WHERE id = $1`, invitationID).Scan(&stored) != nil {
			stored = s.dummyHash()
		}
		s.verify(code, stored)
		return nil, ErrSignInFailed
	}
	if err != nil {
		return nil, err
	}
	if !s.verify(code, hash) {
		if engaged {
			s.auditSignInLocked(ctx, tenantID, sessionID, actor)
		}
		return nil, ErrSignInFailed
	}

	out := &Exchanged{CandidateToken: ids.Token()}
	err = s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		var expires time.Time
		var revoked *time.Time
		var current *string
		if err := tx.QueryRow(ctx, `SELECT expires_at, revoked_at, access_code_hash FROM ati.invitations WHERE id = $1 FOR UPDATE`, invitationID).
			Scan(&expires, &revoked, &current); err != nil {
			return err
		}
		now := s.now().UTC()
		// The invitation may have been revoked or may have expired while the code was checked. The lock
		// is not checked again: this attempt's own reservation may have set it.
		if revoked != nil || !expires.After(now) || current == nil || *current != hash {
			return ErrSignInFailed
		}
		if _, err := tx.Exec(ctx, `UPDATE ati.invitations SET access_failures = 0, access_locked_until = NULL WHERE id = $1`, invitationID); err != nil {
			return err
		}
		return s.openCandidateSession(ctx, tx, after, sess, invitationID, actor, now, out, "candidate.sign_in", "access_code_sign_in")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// acquireVerifier takes a verification slot, waiting at most signInWait. The returned function
// gives the slot back.
func (s *Service) acquireVerifier(ctx context.Context) (func(), error) {
	t := time.NewTimer(s.verifierWait)
	defer t.Stop()
	select {
	case s.verifiers <- struct{}{}:
		return func() { <-s.verifiers }, nil
	case <-t.C:
		return nil, ErrSignInBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dummyHash is verified for unknown application numbers. It uses the same parameters as stored
// hashes, so it costs the same.
func (s *Service) dummyHash() string {
	s.dummyOnce.Do(func() { s.dummy = accesscode.Hash(accesscode.NewCode(), s.settings.AccessCode) })
	return s.dummy
}

// auditSignInLocked records that code sign-in was locked. Single failures are not audited, as with
// invitations.
func (s *Service) auditSignInLocked(ctx context.Context, tenantID, sessionID string, actor Actor) {
	err := s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
		return s.audit(ctx, tx, tenantID, actor, "candidate.sign_in.locked", "interview_session", sessionID, "denied",
			map[string]any{"failures": SignInMaxFailures, "locked_minutes": int(SignInLockDuration / time.Minute)})
	})
	if err != nil && s.log != nil {
		s.log.Error("audit of a sign-in lock failed", "error", err)
	}
}
