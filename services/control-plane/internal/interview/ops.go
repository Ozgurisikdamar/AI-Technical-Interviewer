package interview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// CreateRequest creates an interview for an opaque candidate reference.
type CreateRequest struct {
	TenantID       string
	StaffID        string
	CandidateRef   string
	PlanID         string
	PlanVersion    int
	NoticeLanguage string
	InvitationTTL  time.Duration
}

// Created is returned once. Neither the invitation token nor the access code can be retrieved again.
type Created struct {
	SessionID           string
	InvitationToken     string
	InvitationExpiresAt time.Time
	// ApplicationNumber and AccessCode let the candidate sign in without the link (ADR-024). Both
	// are in canonical form; the code is stored only as a hash.
	ApplicationNumber string
	AccessCode        string
}

// applicationNumberKey is the unique constraint a new application number can, rarely, collide with.
const applicationNumberKey = "interview_sessions_application_number_key"

// CreateInterview creates a session in CREATED with a one-time invitation, an application number
// and an access code. Refused without an explicit retention policy (ADR-020).
func (s *Service) CreateInterview(ctx context.Context, actor Actor, req CreateRequest) (*Created, error) {
	r := s.settings.Retention
	if r.TranscriptDays <= 0 || r.SessionEventsDays <= 0 || r.AssessmentOutputDays <= 0 {
		return nil, ErrRetentionMissing
	}
	if len(req.CandidateRef) == 0 || len(req.CandidateRef) > 128 {
		return nil, &ValidationError{"candidate_ref must be 1-128 characters"}
	}
	plan, err := s.plans.load(ctx, s.pool, req.PlanID, req.PlanVersion)
	if err != nil {
		return nil, err
	}
	lang := req.NoticeLanguage
	if lang == "" {
		lang = plan.Language
	}
	if _, ok := s.notices.Get(s.settings.NoticeID, s.settings.NoticeVersion, lang); !ok {
		return nil, &ValidationError{fmt.Sprintf("no notice %s@%s in %s", s.settings.NoticeID, s.settings.NoticeVersion, lang)}
	}
	ttl := req.InvitationTTL
	if ttl == 0 {
		ttl = s.settings.InvitationTTL
	}
	retention, _ := json.Marshal(map[string]any{
		"transcript_days": r.TranscriptDays, "session_events_days": r.SessionEventsDays,
		"assessment_output_days": r.AssessmentOutputDays, "audio_recording_days": nilIfZero(r.AudioRecordingDays),
	})
	recording, _ := json.Marshal(map[string]bool{"enabled": s.settings.RecordingEnabled, "encryption_attested": s.settings.RecordingAttested})
	out := &Created{SessionID: ids.New(ids.Session), InvitationToken: ids.Token(), AccessCode: accesscode.NewCode()}
	// Hashing is deliberately slow, so it happens before the transaction.
	codeHash := accesscode.Hash(out.AccessCode, s.settings.AccessCode)
	now := s.now().UTC()
	out.InvitationExpiresAt = now.Add(ttl)
	for attempt := 1; ; attempt++ {
		out.ApplicationNumber = s.newNumber()
		err = s.inTx(ctx, func(tx pgx.Tx, _ *[]func(context.Context)) error {
			// INSERT ... DO NOTHING + SELECT keeps the role free of UPDATE on candidates (least privilege).
			if _, err := tx.Exec(ctx, `INSERT INTO ati.candidates (id, tenant_id, external_ref) VALUES ($1,$2,$3)
				ON CONFLICT (tenant_id, external_ref) DO NOTHING`, ids.New(ids.Candidate), req.TenantID, req.CandidateRef); err != nil {
				return err
			}
			var candidateID string
			if err := tx.QueryRow(ctx, `SELECT id FROM ati.candidates WHERE tenant_id = $1 AND external_ref = $2`, req.TenantID, req.CandidateRef).Scan(&candidateID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ati.interview_sessions (id, tenant_id, candidate_id, plan_id, plan_version, notice_id, notice_version,
				notice_language, state, room_name, retention, recording_policy, created_by, created_at, updated_at, application_number)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'CREATED',$9,$10,$11,$12,$13,$13,$14)`, out.SessionID, req.TenantID, candidateID, plan.ID, plan.Version,
				s.settings.NoticeID, s.settings.NoticeVersion, lang, ids.New(ids.Room), retention, recording, req.StaffID, now, out.ApplicationNumber); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ati.invitations (id, session_id, token_hash, created_at, expires_at, access_code_hash) VALUES ($1,$2,$3,$4,$5,$6)`,
				ids.New(ids.Invitation), out.SessionID, ids.Hash(out.InvitationToken), now, out.InvitationExpiresAt, codeHash); err != nil {
				return err
			}
			return s.audit(ctx, tx, req.TenantID, actor, "interview.create", "interview_session", out.SessionID, "allowed",
				map[string]any{"plan_id": plan.ID, "plan_version": plan.Version, "synthetic_plan": plan.Synthetic})
		})
		// Application numbers are random, so a collision is rare; a new number resolves it.
		if attempt < 3 && isUniqueViolation(err, applicationNumberKey) {
			continue
		}
		break
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func nilIfZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// Exchanged is the result of an invitation exchange.
type Exchanged struct {
	SessionID      string
	CandidateToken string
	State          flow.State
}

// ExchangeInvitation turns a valid invitation into a fresh candidate session and revokes earlier
// ones. The first exchange performs T01.
func (s *Service) ExchangeInvitation(ctx context.Context, actor Actor, token string) (*Exchanged, error) {
	out := &Exchanged{CandidateToken: ids.Token()}
	err := s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		var invID, sessionID string
		var expires time.Time
		var revoked *time.Time
		err := tx.QueryRow(ctx, `SELECT session_id FROM ati.invitations WHERE token_hash = $1`, ids.Hash(token)).Scan(&sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		// Lock order: the session first, then its invitation, as revocation does; the other order
		// deadlocks against a concurrent revocation.
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT id, expires_at, revoked_at FROM ati.invitations WHERE token_hash = $1 FOR UPDATE`, ids.Hash(token)).
			Scan(&invID, &expires, &revoked)
		now := s.now().UTC()
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (revoked != nil || !expires.After(now))) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		return s.openCandidateSession(ctx, tx, after, sess, invID, actor, now, out, "invitation.exchange", "invitation_exchanged")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// openCandidateSession ends the interview's earlier candidate sessions and opens a new one, with
// the token in out, through the given invitation; the first one performs T01. Both ways in (the
// invitation link and sign-in) use it, so either ends a session the other opened. The caller
// holds the session's lock, then the invitation's, in that order.
func (s *Service) openCandidateSession(ctx context.Context, tx pgx.Tx, after *[]func(context.Context), sess *sessionRow, invitationID string,
	actor Actor, now time.Time, out *Exchanged, action, cause string) error {
	if _, err := tx.Exec(ctx, `UPDATE ati.candidate_sessions SET revoked_at = $2 WHERE session_id = $1 AND revoked_at IS NULL`, sess.ID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ati.candidate_sessions (id, session_id, token_hash, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		ids.New("csn"), sess.ID, ids.Hash(out.CandidateToken), now, now.Add(s.settings.CandidateSessionTTL)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ati.invitations SET first_exchanged_at = coalesce(first_exchanged_at, $2) WHERE id = $1`, invitationID, now); err != nil {
		return err
	}
	if err := s.audit(ctx, tx, sess.TenantID, actor, action, "interview_session", sess.ID, "allowed", nil); err != nil {
		return err
	}
	if sess.State == flow.Created {
		oc, err := s.newOp(ctx, tx, sess, Actor{Type: flow.ActorSystem, RequestID: actor.RequestID}, after)
		if err != nil {
			return err
		}
		oc.invitationValid, oc.cause = true, cause
		if err := s.fire(ctx, oc, flow.EvInvitationExchanged); err != nil {
			return err
		}
	}
	out.SessionID, out.State = sess.ID, sess.State
	return nil
}

// AuthenticateCandidate resolves an active candidate session token to its interview.
func (s *Service) AuthenticateCandidate(ctx context.Context, token string) (string, error) {
	var sessionID string
	err := s.pool.QueryRow(ctx, `SELECT session_id FROM ati.candidate_sessions WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2`,
		ids.Hash(token), s.now().UTC()).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return sessionID, err
}

// Notice returns the notice pinned to the session plus configured retention days.
func (s *Service) Notice(ctx context.Context, sessionID string) (*Notice, json.RawMessage, error) {
	sess, err := s.getSession(ctx, s.pool, sessionID)
	if err != nil {
		return nil, nil, err
	}
	n, ok := s.notices.Get(sess.NoticeID, sess.NoticeVersion, sess.NoticeLanguage)
	if !ok {
		return nil, nil, fmt.Errorf("pinned notice missing")
	}
	return n, sess.Retention, nil
}

// SubmitConsent records a decision for every purpose of the pinned notice (T02 or T03).
func (s *Service) SubmitConsent(ctx context.Context, actor Actor, sessionID, noticeID, noticeVersion string, decisions map[string]bool) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		notice, ok := s.notices.Get(sess.NoticeID, sess.NoticeVersion, sess.NoticeLanguage)
		if !ok {
			return fmt.Errorf("pinned notice missing")
		}
		in := &consentInput{noticeMatches: noticeID == sess.NoticeID && noticeVersion == sess.NoticeVersion, complete: true, allRequiredGranted: true}
		if !in.noticeMatches {
			return &ValidationError{"decisions must reference the notice version that was displayed"}
		}
		if len(decisions) != len(notice.Purposes) {
			return &ValidationError{"every purpose of the notice needs exactly one decision"}
		}
		var retention map[string]*int
		_ = json.Unmarshal(sess.Retention, &retention)
		for _, p := range notice.Purposes {
			granted, decided := decisions[p.PurposeID]
			if !decided {
				return &ValidationError{"missing decision for purpose " + p.PurposeID}
			}
			if p.Required && !granted {
				in.allRequiredGranted = false
			}
			in.decisions = append(in.decisions, consentDecision{purpose: p, granted: granted, retentionDays: retention[p.RetentionCategory+"_days"]})
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.consent = in
		event := flow.EvConsentAccepted
		if !in.allRequiredGranted {
			event = flow.EvConsentDeclined
		}
		return s.fire(ctx, oc, event)
	})
}

func (s *Service) granted(ctx context.Context, tx pgx.Tx, sess *sessionRow, purpose string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT coalesce(bool_or(granted), false) FROM ati.consent_records WHERE session_id = $1 AND notice_version = $2 AND purpose_id = $3`,
		sess.ID, sess.NoticeVersion, purpose).Scan(&ok)
	return ok, err
}

// Start moves READY → CALIBRATION with a fixed input mode. Voice mode requires the voice purpose.
func (s *Service) Start(ctx context.Context, actor Actor, sessionID string, expected int64, inputMode string) error {
	if inputMode != "voice" && inputMode != "text" {
		return &ValidationError{"input_mode must be voice or text"}
	}
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		if inputMode == "voice" {
			ok, err := s.granted(ctx, tx, sess, "voice_interview_processing")
			if err != nil {
				return err
			}
			if !ok {
				return &ConflictError{"voice_processing_not_consented", "voice mode requires the live voice purpose; use text mode"}
			}
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.inputMode = inputMode
		return s.fire(ctx, oc, flow.EvStart)
	})
}

// CalibrationComplete moves CALIBRATION → QUESTION_ACTIVE (candidate "Begin" or agent confirmation).
func (s *Service) CalibrationComplete(ctx context.Context, actor Actor, lease *LeaseRef, sessionID string, expected int64) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.authorizeActor(ctx, tx, sess, actor, lease); err != nil {
			return err
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		return s.fire(ctx, oc, flow.EvCalibrationComplete)
	})
}

// authorizeActor checks the Tier-1 lease inside the transaction; other actors are authorized by the API.
func (s *Service) authorizeActor(ctx context.Context, tx pgx.Tx, sess *sessionRow, actor Actor, lease *LeaseRef) error {
	if actor.Type != flow.ActorTier1 {
		return nil
	}
	if lease == nil {
		return ErrLeaseStale
	}
	return s.checkLease(ctx, tx, sess, *lease, s.now().UTC())
}

var pauseReasons = map[string]bool{"candidate_request": true, "device_lost": true, "network": true, "agent_unavailable": true, "provider_failure": true, "audio_unavailable": true}

// Pause pauses from CALIBRATION, QUESTION_ACTIVE or CANDIDATE_SPEAKING. expected is nil for
// system-initiated pauses (webhook disconnects, lease expiry), which act on the current version.
func (s *Service) Pause(ctx context.Context, actor Actor, lease *LeaseRef, sessionID string, expected *int64, reason string) error {
	if !pauseReasons[reason] {
		return &ValidationError{"unknown pause reason"}
	}
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := s.authorizeActor(ctx, tx, sess, actor, lease); err != nil {
			return err
		}
		if expected != nil {
			if err := checkVersion(sess, *expected); err != nil {
				return err
			}
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.pauseReason, oc.closeReason, oc.cause = reason, "paused", reason
		return s.fire(ctx, oc, flow.EvPause)
	})
}

// Resume returns to the same question (new activation) or to calibration.
func (s *Service) Resume(ctx context.Context, actor Actor, sessionID string, expected int64) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		return s.fire(ctx, oc, flow.EvResume)
	})
}

// Withdraw cancels the interview at the candidate's request.
func (s *Service) Withdraw(ctx context.Context, actor Actor, sessionID string, expected int64) error {
	return s.inTx(ctx, func(tx pgx.Tx, after *[]func(context.Context)) error {
		sess, err := s.lockSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if err := checkVersion(sess, expected); err != nil {
			return err
		}
		oc, err := s.newOp(ctx, tx, sess, actor, after)
		if err != nil {
			return err
		}
		oc.closeReason, oc.cause = "withdrawn", "candidate_withdrawal"
		return s.fire(ctx, oc, flow.EvWithdraw)
	})
}
