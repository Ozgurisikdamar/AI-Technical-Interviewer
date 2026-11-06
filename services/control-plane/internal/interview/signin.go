package interview

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// Candidate sign-in limits (ADR-024).
const (
	// SignInMaxFailures wrong codes in a row, each within SignInLockDuration of the one before, lock
	// code sign-in for SignInLockDuration. Wrong codes further apart start the count again, so old
	// typos never add up. The lock does not extend while it lasts, and the invitation link keeps
	// working.
	SignInMaxFailures  = 5
	SignInLockDuration = 15 * time.Minute
	// signInVerifiers bounds concurrent argon2 verifications: each holds about 19 MiB for tens of
	// milliseconds. A sign-in waits at most signInWait for a slot, then gets ErrSignInBusy.
	signInVerifiers = 4
	signInWait      = 3 * time.Second
	// forgiveTimeout bounds the clean-up after a right code whose session could not be opened.
	forgiveTimeout = 5 * time.Second
)

// reserveAttempt finds the newest invitation of an application number that has an access code and,
// in the same statement, counts the attempt, unless the invitation is revoked, expired or locked.
// The attempt that would be the last one allowed engages the lock instead, and the count starts
// again, so the first wrong code after the lock does not lock again at once. A count whose previous
// attempt is older than the lock period starts again from one.
// $1 number, $2 now, $3 the end of a lock that engages now, $4 SignInMaxFailures, $5 now minus the
// lock period. No row: the number is unknown. counted false: revoked, expired or locked.
const reserveAttempt = `WITH target AS (
		SELECT i.id, s.id AS session_id, s.tenant_id, i.access_code_hash
		FROM ati.interview_sessions s JOIN ati.invitations i ON i.session_id = s.id
		WHERE s.application_number = $1 AND i.access_code_hash IS NOT NULL
		ORDER BY i.created_at DESC, i.id LIMIT 1
	), counted AS (
		UPDATE ati.invitations i
		SET access_failures = CASE WHEN i.access_failed_at > $5 AND i.access_failures >= $4 - 1 THEN 0
		                           WHEN i.access_failed_at > $5 THEN i.access_failures + 1
		                           ELSE 1 END,
		    access_locked_until = CASE WHEN i.access_failed_at > $5 AND i.access_failures >= $4 - 1 THEN $3::timestamptz END,
		    access_failed_at = $2
		FROM target
		WHERE i.id = target.id AND i.revoked_at IS NULL AND i.expires_at > $2
		  AND (i.access_locked_until IS NULL OR i.access_locked_until <= $2)
		RETURNING i.id, i.access_locked_until IS NOT NULL AS engaged
	)
	SELECT target.id, target.session_id, target.tenant_id, target.access_code_hash, counted.id IS NOT NULL, coalesce(counted.engaged, false)
	FROM target LEFT JOIN counted ON counted.id = target.id`

// SignInCandidate opens a candidate session from an application number and an access code
// (ADR-024).
//
// The code is a second credential of the interview's invitation, so the invitation's expiry and
// revocation apply to it, and a success behaves exactly like an invitation exchange.
//
// Every refusal is the same ErrSignInFailed: an unknown number, a wrong code, an expired or revoked
// invitation, and a lock alike. Past the format check (the format is public), every attempt
// verifies exactly one argon2 hash, against a dummy one when the number is unknown, so the costly
// work does not tell the reasons apart. The database work differs only in that a known number's
// attempt is counted (ADR-024 records this residual difference).
//
// An attempt is counted before its code is verified, so parallel guesses cannot exceed the limit.
// A verification slot is held only while the attempt is looked up, counted and verified, never
// while the session opens.
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

	// Microseconds, as PostgreSQL stores them: forgiveAttempt finds this attempt by its time.
	now := s.now().UTC().Truncate(time.Microsecond)
	var invitationID, sessionID, tenantID, hash string
	var counted, engaged bool
	err = s.pool.QueryRow(ctx, reserveAttempt, number, now, now.Add(SignInLockDuration), SignInMaxFailures, now.Add(-SignInLockDuration)).
		Scan(&invitationID, &sessionID, &tenantID, &hash, &counted, &engaged)
	if errors.Is(err, pgx.ErrNoRows) {
		s.verify(code, s.dummy)
		return nil, ErrSignInFailed
	}
	if err != nil {
		return nil, err
	}
	matched := s.verify(code, hash)
	release()
	if !matched {
		if engaged {
			s.auditSignInLocked(ctx, tenantID, sessionID, actor)
		}
		return nil, ErrSignInFailed
	}
	if !counted { // revoked, expired or locked: refused whatever the code
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
		if _, err := tx.Exec(ctx, `UPDATE ati.invitations SET access_failures = 0, access_failed_at = NULL, access_locked_until = NULL WHERE id = $1`,
			invitationID); err != nil {
			return err
		}
		return s.openCandidateSession(ctx, tx, after, sess, invitationID, actor, now, out, "candidate.sign_in", "access_code_sign_in")
	})
	if err != nil {
		if !errors.Is(err, ErrSignInFailed) {
			s.forgiveAttempt(ctx, invitationID, now)
		}
		return nil, err
	}
	return out, nil
}

// forgiveAttempt undoes what an attempt with the right code counted when its session could not be
// opened (a transient failure, or the client going away), so the candidate is not locked out for
// knowing the code. It leaves the count alone if a later attempt has counted since. It runs even
// when the request's context has ended.
func (s *Service) forgiveAttempt(ctx context.Context, invitationID string, attemptAt time.Time) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), forgiveTimeout)
	defer cancel()
	if _, err := s.pool.Exec(ctx, `UPDATE ati.invitations SET access_failures = 0, access_failed_at = NULL, access_locked_until = NULL
		WHERE id = $1 AND access_failed_at = $2`, invitationID, attemptAt); err != nil && s.log != nil {
		s.log.Error("clearing the count of a sign-in that could not open a session failed", "error", err)
	}
}

// acquireVerifier takes a verification slot, waiting at most s.verifierWait. The returned function
// gives the slot back; calling it again does nothing.
func (s *Service) acquireVerifier(ctx context.Context) (func(), error) {
	t := time.NewTimer(s.verifierWait)
	defer t.Stop()
	select {
	case s.verifiers <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-s.verifiers }) }, nil
	case <-t.C:
		return nil, ErrSignInBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
