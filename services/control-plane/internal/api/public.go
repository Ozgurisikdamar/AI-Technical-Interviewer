package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
)

// candidate authenticates the cookie and binds it to the interview in the path (404 otherwise).
func (s *Server) candidate(w http.ResponseWriter, r *http.Request) (principal, bool) {
	sid, ok := s.authCandidate(r)
	if !ok {
		unauthorized(w, r)
		return principal{}, false
	}
	if id := r.PathValue("interview_id"); id != "" && id != sid {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "not found")
		return principal{}, false
	}
	return principal{kind: kindCandidate, sessionID: sid}, true
}

func (s *Server) writeCandidateView(w http.ResponseWriter, r *http.Request, status int, sessionID string) {
	v, err := s.svc.CandidateView(r.Context(), sessionID, s.cfg.Env)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, v)
}

// mutate runs a candidate mutation with idempotency, strict decoding and a fresh view on success.
func mutate[T any](s *Server, w http.ResponseWriter, r *http.Request, status int, fn func(p principal, body *T) error) {
	p, ok := s.candidate(w, r)
	if !ok {
		return
	}
	s.idempotent(w, r, p.idemKey(), func(w http.ResponseWriter, r *http.Request) {
		var body T
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(w, r, &body, maxPublicBody); err != nil {
				httpx.WriteDecodeError(w, r, err)
				return
			}
		}
		if err := fn(p, &body); err != nil {
			s.writeError(w, r, err)
			return
		}
		s.writeCandidateView(w, r, status, p.sessionID)
	})
}

type createInterviewRequest struct {
	CandidateRef         string `json:"candidate_ref"`
	PlanID               string `json:"plan_id"`
	PlanVersion          int    `json:"plan_version"`
	NoticeLanguage       string `json:"notice_language"`
	InvitationTTLMinutes int    `json:"invitation_ttl_minutes"`
}

func (s *Server) createInterview(w http.ResponseWriter, r *http.Request) {
	staff, ok := s.authStaff(r)
	if !ok {
		unauthorized(w, r)
		return
	}
	if staff.Role != "admin" && staff.Role != "recruiter" {
		httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "role not permitted")
		return
	}
	p := principal{kind: kindStaff, staff: staff}
	s.idempotent(w, r, p.idemKey(), func(w http.ResponseWriter, r *http.Request) {
		var req createInterviewRequest
		if err := httpx.DecodeJSON(w, r, &req, maxPublicBody); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		if req.InvitationTTLMinutes != 0 && (req.InvitationTTLMinutes < 5 || req.InvitationTTLMinutes > 10080) {
			httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "invitation_ttl_minutes must be between 5 and 10080")
			return
		}
		created, err := s.svc.CreateInterview(r.Context(), actorFor(r.Context(), p), interview.CreateRequest{
			TenantID: staff.TenantID, StaffID: staff.TokenID, CandidateRef: req.CandidateRef, PlanID: req.PlanID, PlanVersion: req.PlanVersion,
			NoticeLanguage: req.NoticeLanguage, InvitationTTL: time.Duration(req.InvitationTTLMinutes) * time.Minute,
		})
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		view, err := s.svc.StaffView(r.Context(), created.SessionID, staff.TenantID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{
			"interview":             view,
			"invitation_url":        s.cfg.PublicOrigin.String() + "/i#token=" + created.InvitationToken,
			"invitation_expires_at": created.InvitationExpiresAt,
		})
	})
}

func (s *Server) getInterview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("interview_id")
	if staff, ok := s.authStaff(r); ok {
		v, err := s.svc.StaffView(r.Context(), id, staff.TenantID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, v)
		return
	}
	p, ok := s.candidate(w, r)
	if !ok {
		return
	}
	s.writeCandidateView(w, r, http.StatusOK, p.sessionID)
}

func (s *Server) exchangeInvitation(w http.ResponseWriter, r *http.Request) {
	ip := httpx.ClientIP(r, s.cfg.TrustedProxies)
	if !s.limiter.allow(ip, s.now()) {
		httpx.WriteProblem(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts; wait and try again")
		return
	}
	if !idemKeyPattern.MatchString(r.Header.Get("Idempotency-Key")) {
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeIdempotencyKeyRequired, "a valid Idempotency-Key header is required")
		return
	}
	// Not replayed from storage: each execution yields a fresh session cookie and revokes the
	// previous one, so a retry is safe and no session token is ever stored (ADR-016).
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &req, maxPublicBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if len(req.Token) < 32 || len(req.Token) > 128 {
		httpx.WriteProblem(w, r, http.StatusGone, httpx.CodeInvitationInvalid, "the invitation is invalid or has expired")
		return
	}
	ex, err := s.svc.ExchangeInvitation(r.Context(), interview.Actor{Type: flow.ActorCandidate, RequestID: httpx.RequestIDFrom(r.Context())}, req.Token)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CandidateCookie, Value: ex.CandidateToken, Path: "/api", HttpOnly: true, Secure: s.cfg.SecureCookies(),
		SameSite: http.SameSiteStrictMode, MaxAge: int(s.cfg.CandidateSessionTTL / time.Second),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"interview_id": ex.SessionID, "state": ex.State})
}

func (s *Server) getConsentNotice(w http.ResponseWriter, r *http.Request) {
	p, ok := s.candidate(w, r)
	if !ok {
		return
	}
	notice, retention, err := s.svc.Notice(r.Context(), p.sessionID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var days map[string]*int
	_ = json.Unmarshal(retention, &days)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"notice": json.RawMessage(notice.Raw),
		"retention_days": map[string]*int{
			"transcript": days["transcript_days"], "session_events": days["session_events_days"],
			"assessment_output": days["assessment_output_days"], "audio_recording": days["audio_recording_days"],
		},
	})
}

type consentBody struct {
	NoticeID      string `json:"notice_id"`
	NoticeVersion string `json:"notice_version"`
	Decisions     []struct {
		PurposeID string `json:"purpose_id"`
		Granted   bool   `json:"granted"`
	} `json:"decisions"`
}

func (s *Server) submitConsent(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *consentBody) error {
		decisions := map[string]bool{}
		for _, d := range b.Decisions {
			if _, dup := decisions[d.PurposeID]; dup {
				return &interview.ValidationError{Msg: "duplicate decision for " + d.PurposeID}
			}
			decisions[d.PurposeID] = d.Granted
		}
		return s.svc.SubmitConsent(r.Context(), actorFor(r.Context(), p), p.sessionID, b.NoticeID, b.NoticeVersion, decisions)
	})
}

type versionBody struct {
	StateVersion int64 `json:"state_version"`
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *struct {
		StateVersion int64  `json:"state_version"`
		InputMode    string `json:"input_mode"`
	}) error {
		return s.svc.Start(r.Context(), actorFor(r.Context(), p), p.sessionID, b.StateVersion, b.InputMode)
	})
}

func (s *Server) candidateCalibrationComplete(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *versionBody) error {
		return s.svc.CalibrationComplete(r.Context(), actorFor(r.Context(), p), nil, p.sessionID, b.StateVersion)
	})
}

func (s *Server) candidatePause(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *struct {
		StateVersion int64  `json:"state_version"`
		Reason       string `json:"reason"`
	}) error {
		if b.Reason != "candidate_request" && b.Reason != "device_lost" {
			return &interview.ValidationError{Msg: "reason must be candidate_request or device_lost"}
		}
		v := b.StateVersion
		return s.svc.Pause(r.Context(), actorFor(r.Context(), p), nil, p.sessionID, &v, b.Reason)
	})
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *versionBody) error {
		if err := s.svc.Resume(r.Context(), actorFor(r.Context(), p), p.sessionID, b.StateVersion); err != nil {
			return err
		}
		// The interviewer may have been lost (crash, provider failure); a resumed interview needs one.
		// Best effort: if no agent claims in time, the missing-agent sweeper pauses again (ADR-016).
		if err := s.ensureInterviewer(r.Context(), p.sessionID); err != nil {
			s.log.Warn("agent dispatch after resume failed", "session_id", p.sessionID, "error", err)
		}
		return nil
	})
}

func (s *Server) candidateAnswerComplete(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusAccepted, func(p principal, b *struct {
		StateVersion int64  `json:"state_version"`
		ActivationID string `json:"activation_id"`
	}) error {
		_, err := s.svc.RequestAnswerCompletion(r.Context(), actorFor(r.Context(), p), p.sessionID, b.StateVersion, b.ActivationID)
		return err
	})
}

func (s *Server) confirmComplete(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, _ *struct{}) error {
		_, state, _, err := s.svc.RoomFor(r.Context(), p.sessionID)
		if err != nil {
			return err
		}
		switch state {
		case flow.InterviewCompleted, flow.EvaluationPending, flow.HumanReview, flow.ReportReady:
			return nil // completion happens automatically after the last answer; this only confirms it
		}
		return &interview.TransitionRefusedError{State: state, Event: "complete", Err: flow.ErrNoTransition}
	})
}

func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	mutate(s, w, r, http.StatusOK, func(p principal, b *struct {
		StateVersion int64 `json:"state_version"`
		Confirm      bool  `json:"confirm"`
	}) error {
		if !b.Confirm {
			return &interview.ValidationError{Msg: "confirm must be true"}
		}
		return s.svc.Withdraw(r.Context(), actorFor(r.Context(), p), p.sessionID, b.StateVersion)
	})
}

var mediaStates = map[flow.State]bool{flow.Calibration: true, flow.QuestionActive: true, flow.CandidateSpeaking: true, flow.Paused: true}

func (s *Server) realtimeSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.candidate(w, r)
	if !ok {
		return
	}
	s.idempotent(w, r, p.idemKey(), func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		room, state, mode, err := s.svc.RoomFor(ctx, p.sessionID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if !mediaStates[state] {
			httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeTransitionNotAllowed, "the interview is not live")
			return
		}
		if err := s.ensureInterviewer(ctx, p.sessionID); err != nil {
			s.log.Warn("media setup failed", "session_id", p.sessionID, "error", err)
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "media service or interviewer unavailable")
			return
		}
		token, expires, err := s.media.CandidateToken(room, mode == "voice")
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"livekit_url": s.media.PublicURL(), "token": token, "room": room, "identity": realtime.CandidateIdentity,
			"can_publish_audio": mode == "voice", "expires_at": expires.UTC(),
		})
	})
}

// ensureInterviewer makes sure the session's room exists and an agent holds or is about to claim
// the session (explicit dispatch only; ADR-011, ADR-016).
func (s *Server) ensureInterviewer(ctx context.Context, sessionID string) error {
	room, _, _, err := s.svc.RoomFor(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.media.EnsureRoom(ctx, room); err != nil {
		return fmt.Errorf("ensure room: %w", err)
	}
	if _, err := s.svc.EnsureAgentDispatch(ctx, sessionID, interview.MediaOps{RemoveStaleAgents: s.removeStaleAgents(sessionID), Dispatch: s.media.Dispatch}); err != nil {
		return fmt.Errorf("agent dispatch: %w", err)
	}
	return nil
}

// removeStaleAgents removes agent participants that do not hold the session's lease, so a
// re-dispatch never leaves two agents writing to one interview (ADR-016).
func (s *Server) removeStaleAgents(sessionID string) func(ctx context.Context, room string) error {
	return func(ctx context.Context, room string) error {
		active, err := s.svc.ActiveAgentIdentity(ctx, sessionID)
		if err != nil {
			return err
		}
		parts, err := s.media.ListParticipants(ctx, room)
		if err != nil {
			return err
		}
		for _, p := range parts {
			if p.Agent && p.Identity != active {
				if err := s.media.RemoveParticipant(ctx, room, p.Identity); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// textResponse stores a text-mode answer (ADR-018). The text never travels over LiveKit.
func (s *Server) textResponse(w http.ResponseWriter, r *http.Request) {
	p, ok := s.candidate(w, r)
	if !ok {
		return
	}
	s.idempotent(w, r, p.idemKey(), func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ActivationID string `json:"activation_id"`
			Text         string `json:"text"`
		}
		if err := httpx.DecodeJSON(w, r, &body, maxPublicBody); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		res, err := s.svc.SubmitTextResponse(r.Context(), actorFor(r.Context(), p), p.sessionID, body.ActivationID, body.Text)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, res)
	})
}
