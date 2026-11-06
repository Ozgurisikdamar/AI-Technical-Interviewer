// Package api serves the control-plane HTTP API on two listeners (ADR-016): the public listener
// (/api/v1, behind the same-origin web rewrite) and the internal listener (/internal/v1 Tier-1 API,
// LiveKit webhook, health and metrics), which must never be routed publicly.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

const (
	maxPublicBody   = 16 << 10
	maxInternalBody = 128 << 10
)

// Deps are the server dependencies.
type Deps struct {
	Config    *config.Config
	Service   *interview.Service
	Tier1     *tier1ctx.Builder
	Media     realtime.Media
	Pool      *pgxpool.Pool // control-plane role
	Tier1Pool *pgxpool.Pool // Tier-1 context role (readiness only)
	Log       *slog.Logger
	Metrics   *Metrics
	Now       func() time.Time
	// Recorder is nil when recording is disabled (the default, ADR-017).
	Recorder Recorder
	// Relay publishes the outbox to Tier-2; nil without ATI_REDIS_URL (ADR-022).
	Relay Relay
}

// Relay publishes outbox events (internal/outbox).
type Relay interface {
	RunOnce(ctx context.Context) (int, error)
}

// Server holds handlers for both listeners.
type Server struct {
	cfg       *config.Config
	svc       *interview.Service
	tier1     *tier1ctx.Builder
	media     realtime.Media
	pool      *pgxpool.Pool
	tier1Pool *pgxpool.Pool
	log       *slog.Logger
	metrics   *Metrics
	now       func() time.Time
	limiter   *ipLimiter // invitation exchanges
	// staffLimiter bounds staff sign-ins per client, and candidateLimiter candidate sign-ins. Behind
	// a proxy, ATI_TRUSTED_PROXY_CIDRS must name it, or every client shares the proxy's budget
	// (ADR-023, ADR-024).
	staffLimiter     *ipLimiter
	candidateLimiter *ipLimiter
	rec              Recorder
	relay            Relay
}

// New builds the server.
func New(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Metrics == nil {
		d.Metrics = NewMetrics()
	}
	return &Server{cfg: d.Config, svc: d.Service, tier1: d.Tier1, media: d.Media, pool: d.Pool, tier1Pool: d.Tier1Pool,
		log: d.Log, metrics: d.Metrics, now: d.Now, limiter: newIPLimiter(d.Config.InvitationExchangesPerMinute),
		staffLimiter: newIPLimiter(d.Config.StaffSignInsPerMinute), candidateLimiter: newIPLimiter(d.Config.CandidateSignInsPerMinute),
		rec: d.Recorder, relay: d.Relay}
}

// Route is one registered endpoint.
type Route struct {
	Method  string
	Pattern string
	handler func(*Server, http.ResponseWriter, *http.Request)
}

var publicRoutes = []Route{
	{"POST", "/api/v1/interviews", (*Server).createInterview},
	{"GET", "/api/v1/interviews/{interview_id}", (*Server).getInterview},
	{"POST", "/api/v1/interviews/{interview_id}/invitation/revoke", (*Server).revokeInvitation},
	{"POST", "/api/v1/invitations/exchange", (*Server).exchangeInvitation},
	{"POST", "/api/v1/candidate/session", (*Server).startCandidateSession},
	{"GET", "/api/v1/interviews/{interview_id}/consent-notice", (*Server).getConsentNotice},
	{"POST", "/api/v1/interviews/{interview_id}/consent", (*Server).submitConsent},
	{"POST", "/api/v1/interviews/{interview_id}/start", (*Server).start},
	{"POST", "/api/v1/interviews/{interview_id}/calibration-complete", (*Server).candidateCalibrationComplete},
	{"POST", "/api/v1/interviews/{interview_id}/pause", (*Server).candidatePause},
	{"POST", "/api/v1/interviews/{interview_id}/resume", (*Server).resume},
	{"POST", "/api/v1/interviews/{interview_id}/answer-complete", (*Server).candidateAnswerComplete},
	{"POST", "/api/v1/interviews/{interview_id}/complete", (*Server).confirmComplete},
	{"POST", "/api/v1/interviews/{interview_id}/withdraw", (*Server).withdraw},
	{"POST", "/api/v1/interviews/{interview_id}/realtime-session", (*Server).realtimeSession},
	{"POST", "/api/v1/interviews/{interview_id}/text-responses", (*Server).textResponse},
	{"GET", "/api/v1/interviews/{interview_id}/report", (*Server).getReport},
	{"GET", "/api/v1/reports", (*Server).listReports},
	{"POST", "/api/v1/staff/session", (*Server).startStaffSession},
	{"GET", "/api/v1/staff/session", (*Server).getStaffSession},
	{"POST", "/api/v1/staff/session/end", (*Server).endStaffSession},
	{"GET", "/api/v1/review-tasks", (*Server).listReviewTasks},
	{"GET", "/api/v1/review-tasks/{task_id}", (*Server).getReviewTask},
	{"POST", "/api/v1/review-tasks/{task_id}/outcome", (*Server).submitReviewOutcome},
}

var internalRoutes = []Route{
	{"POST", "/internal/v1/tier1/claims", (*Server).tier1Claim},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/lease/renew", (*Server).tier1Renew},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/lease/release", (*Server).tier1Release},
	{"GET", "/internal/v1/tier1/sessions/{session_id}/context", (*Server).tier1Context},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/transcript-segments", (*Server).tier1Segment},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/prompt-started", (*Server).tier1PromptStarted},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/calibration-complete", (*Server).tier1CalibrationComplete},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/answer-complete", (*Server).tier1AnswerComplete},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/pause", (*Server).tier1Pause},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/telemetry", (*Server).tier1Telemetry},
	{"POST", "/internal/v1/tier1/sessions/{session_id}/usage", (*Server).tier1Usage},
	{"POST", "/internal/v1/tier2/sessions/{session_id}/evaluations", (*Server).tier2RecordEvaluation},
	{"POST", "/internal/v1/tier2/sessions/{session_id}/complete", (*Server).tier2Complete},
	{"POST", "/internal/v1/livekit/webhook", (*Server).livekitWebhook},
	{"GET", "/healthz", (*Server).healthz},
	{"GET", "/readyz", (*Server).readyz},
	{"GET", "/metrics", (*Server).metricsHandler},
}

// PublicRoutes and InternalRoutes expose the route tables (OpenAPI drift test).
func PublicRoutes() []Route   { return publicRoutes }
func InternalRoutes() []Route { return internalRoutes }

func (s *Server) mux(routes []Route) *http.ServeMux {
	m := http.NewServeMux()
	for _, rt := range routes {
		h := rt.handler
		m.HandleFunc(rt.Method+" "+rt.Pattern, func(w http.ResponseWriter, r *http.Request) { h(s, w, r) })
	}
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "no such endpoint")
	})
	return m
}

// PublicHandler serves /api/v1 with the same-origin guard.
func (s *Server) PublicHandler() http.Handler {
	var h http.Handler = s.mux(publicRoutes)
	h = httpx.WithSameOrigin(s.cfg.PublicOrigin, h)
	h = httpx.WithRecoveryAndAccessLog(s.log, s.metrics.Observer("public"), h)
	h = httpx.WithAPIHeaders(s.cfg.Env, h)
	return httpx.WithRequestID(h)
}

// InternalHandler serves the Tier-1 API, webhook, health and metrics.
func (s *Server) InternalHandler() http.Handler {
	var h http.Handler = s.mux(internalRoutes)
	h = httpx.WithRecoveryAndAccessLog(s.log, s.metrics.Observer("internal"), h)
	h = httpx.WithAPIHeaders(s.cfg.Env, h)
	return httpx.WithRequestID(h)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "database unavailable")
		return
	}
	if s.tier1Pool != nil {
		if err := s.tier1Pool.Ping(ctx); err != nil {
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "tier-1 context database unavailable")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	s.metrics.Handler().ServeHTTP(w, r)
}

// Effects implements interview.Effects with the media layer.
type Effects struct {
	Media realtime.Media
	Log   *slog.Logger
	Rec   Recorder
	// Background tracks asynchronous effects so shutdown can wait for them (nil: untracked).
	Background *Background
}

// Recorder starts and stops consented recordings (ADR-017); nil disables recording.
type Recorder interface {
	Start(ctx context.Context, sessionID string)
	Stop(ctx context.Context, sessionID string)
	OnEgressEvent(ctx context.Context, ev *realtime.Webhook) error
	// Reconcile settles recordings left starting or stopping by a restart or a lost webhook.
	Reconcile(ctx context.Context) (int, error)
}

func (e Effects) DeleteRoom(ctx context.Context, sessionID, room string) {
	if err := e.Media.DeleteRoom(ctx, room); err != nil {
		e.Log.Warn("delete room failed", slog.String("session_id", sessionID), slog.Any("error", err))
	}
}

func (e Effects) StopRecording(ctx context.Context, sessionID string) {
	if e.Rec != nil {
		e.Rec.Stop(ctx, sessionID)
	}
}

// StartRecording does not hold up the request that opened the question (the agent's calibration,
// the candidate's resume): an egress service that is slow or absent delays only the recording,
// which the recorder bounds and fails closed. Start serializes with transitions on the session row.
func (e Effects) StartRecording(ctx context.Context, sessionID string) {
	if e.Rec != nil {
		ctx := context.WithoutCancel(ctx)
		e.Background.Go(func() { e.Rec.Start(ctx, sessionID) })
	}
}
