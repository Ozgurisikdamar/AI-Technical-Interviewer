package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

// writeError maps service errors to stable problem codes. Unknown errors are logged (without
// content) and returned as a generic 500.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		refused  *interview.TransitionRefusedError
		conflict *interview.ConflictError
		invalid  *interview.ValidationError
		rejected *interview.SegmentRejectedError
	)
	switch {
	case errors.Is(err, interview.ErrNotFound), errors.Is(err, tier1ctx.ErrNotAvailable):
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "not found")
	case errors.Is(err, interview.ErrStaleStateVersion):
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeStaleStateVersion, "the interview changed; refresh and retry")
	case errors.Is(err, interview.ErrInvitationInvalid):
		httpx.WriteProblem(w, r, http.StatusGone, httpx.CodeInvitationInvalid, "the invitation is invalid or has expired")
	case errors.Is(err, interview.ErrRetentionMissing):
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, httpx.CodeRetentionMissing, "interviews cannot be created until explicit retention periods are configured")
	case errors.Is(err, interview.ErrPlanUnavailable):
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodePlanUnavailable, "plan not found or not published")
	case errors.Is(err, interview.ErrLeaseStale):
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeLeaseStale, "the agent lease is not current")
	case errors.Is(err, interview.ErrSegmentConflict):
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeSegmentConflict, "segment key reused with different content")
	case errors.Is(err, interview.ErrManifestIncomplete):
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeManifestIncomplete, "not every flushed segment has been received yet")
	case errors.Is(err, interview.ErrNonceInvalid):
		httpx.WriteProblem(w, r, http.StatusGone, httpx.CodeNonceInvalid, "claim nonce invalid, used or expired")
	case errors.As(err, &refused):
		httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeTransitionNotAllowed, "this action is not available in the current interview state")
	case errors.As(err, &conflict):
		httpx.WriteProblem(w, r, http.StatusConflict, conflict.Code, conflict.Msg)
	case errors.As(err, &invalid):
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, invalid.Msg)
	case errors.As(err, &rejected):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, httpx.CodeSegmentRejected, rejected.Reason)
	default:
		s.log.Error("request failed", slog.String("request_id", httpx.RequestIDFrom(r.Context())), slog.String("route", r.Pattern), slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}
