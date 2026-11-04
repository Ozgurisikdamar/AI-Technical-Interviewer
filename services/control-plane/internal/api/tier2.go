package api

// Phase 2 endpoints (ADR-022): the Tier-2 worker reports evaluator attempts and completion on the
// internal listener; staff review answers and read reports on the public listener. No endpoint
// exposes a hiring decision, and candidates see none of this.

import (
	"net/http"
	"strconv"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
)

// maxTier2Body bounds an evaluation report: the evaluator output plus its envelope.
const maxTier2Body = interview.MaxEvaluatorOutput + 16<<10

// authTier2 checks the Tier-2 service credential in constant time. Without a configured token
// every request is refused.
func (s *Server) authTier2(w http.ResponseWriter, r *http.Request) bool {
	tok := bearer(r)
	if s.cfg.Tier2ServiceTokenSHA256 == "" || tok == "" || !ids.Equal(ids.Hash(tok), s.cfg.Tier2ServiceTokenSHA256) {
		unauthorized(w, r)
		return false
	}
	return true
}

func tier2Actor(r *http.Request) interview.Actor {
	a := interview.Tier2Actor
	a.RequestID = httpx.RequestIDFrom(r.Context())
	return a
}

func (s *Server) tier2RecordEvaluation(w http.ResponseWriter, r *http.Request) {
	if !s.authTier2(w, r) {
		return
	}
	var sub interview.EvaluationSubmission
	if err := httpx.DecodeJSON(w, r, &sub, maxTier2Body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	res, err := s.svc.RecordEvaluation(r.Context(), tier2Actor(r), r.PathValue("session_id"), sub)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Outcome == "duplicate" {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (s *Server) tier2Complete(w http.ResponseWriter, r *http.Request) {
	if !s.authTier2(w, r) {
		return
	}
	var body struct {
		FailureReason *string `json:"failure_reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body, maxInternalBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	failure := ""
	if body.FailureReason != nil {
		failure = *body.FailureReason
	}
	res, err := s.svc.CompleteEvaluation(r.Context(), tier2Actor(r), r.PathValue("session_id"), failure)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Reviewers and administrators decide reviews; recruiters, reviewers and administrators read reports.
var (
	reviewRoles = []string{"reviewer", "admin"}
	reportRoles = []string{"admin", "recruiter", "reviewer"}
)

func (s *Server) listReviewTasks(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffWith(w, r, bearerOrSession, reviewRoles...)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	tasks, err := s.svc.ReviewQueue(r.Context(), p.staff.TenantID, status, r.URL.Query().Get("interview_id"), limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"review_tasks": tasks})
}

// queryLimit reads the optional limit parameter of a list (1 to 200, default 50).
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 200 {
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "limit must be between 1 and 200")
		return 0, false
	}
	return n, true
}

// listReports lists the tenant's reports without their content: no scores, no candidate identity.
func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffWith(w, r, bearerOrSession, reportRoles...)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	reports, err := s.svc.ListReports(r.Context(), p.staff.TenantID, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

func (s *Server) getReviewTask(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffWith(w, r, bearerOrSession, reviewRoles...)
	if !ok {
		return
	}
	task, err := s.svc.ReviewTask(r.Context(), p.staff.TenantID, r.PathValue("task_id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, task)
}

func (s *Server) submitReviewOutcome(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffWith(w, r, bearerOrSession, reviewRoles...)
	if !ok {
		return
	}
	s.idempotent(w, r, p.idemKey(), func(w http.ResponseWriter, r *http.Request) {
		var dec interview.ReviewDecision
		if err := httpx.DecodeJSON(w, r, &dec, maxPublicBody); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		actor := actorFor(r.Context(), p)
		actor.Type = flow.ActorReviewer // T17 belongs to the reviewer, not the system
		res, err := s.svc.SubmitReviewOutcome(r.Context(), actor, p.staff.TenantID, p.staff.TokenID, r.PathValue("task_id"), dec)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, res)
	})
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.staffWith(w, r, bearerOrSession, reportRoles...)
	if !ok {
		return
	}
	body, err := s.svc.Report(r.Context(), p.staff.TenantID, r.PathValue("interview_id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
