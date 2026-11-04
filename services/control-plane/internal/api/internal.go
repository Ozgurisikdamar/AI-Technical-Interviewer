package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
)

// agent authenticates the service credential plus the per-session lease.
func (s *Server) agent(w http.ResponseWriter, r *http.Request) (principal, bool) {
	sid := r.PathValue("session_id")
	lease, ok := s.authAgentLease(r, sid)
	if !ok {
		unauthorized(w, r)
		return principal{}, false
	}
	return principal{kind: kindAgent, sessionID: sid, lease: lease}, true
}

func (s *Server) writeTier1Context(w http.ResponseWriter, r *http.Request, status int, sessionID string) {
	c, err := s.tier1.Build(r.Context(), sessionID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, c)
}

// agentOp decodes a body, runs fn, and answers with the latest Tier-1 context.
func agentOp[T any](s *Server, w http.ResponseWriter, r *http.Request, fn func(p principal, body *T) error) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	var body T
	if err := httpx.DecodeJSON(w, r, &body, maxInternalBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if err := fn(p, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeTier1Context(w, r, http.StatusOK, p.sessionID)
}

func (s *Server) tier1Claim(w http.ResponseWriter, r *http.Request) {
	if !s.authAgentService(r) {
		unauthorized(w, r)
		return
	}
	var req struct {
		SessionID     string `json:"session_id"`
		ClaimNonce    string `json:"claim_nonce"`
		JobID         string `json:"job_id"`
		AgentIdentity string `json:"agent_identity"`
	}
	if err := httpx.DecodeJSON(w, r, &req, maxPublicBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	verify := func(ctx context.Context, room, dispatchID string) error {
		dispatches, err := s.media.ListDispatches(ctx, room)
		if err != nil {
			return err
		}
		ok := slices.ContainsFunc(dispatches, func(d realtime.Dispatch) bool {
			var m interview.DispatchMetadata
			return d.ID == dispatchID && json.Unmarshal([]byte(d.Metadata), &m) == nil && m.SessionID == req.SessionID && m.ClaimNonce == req.ClaimNonce
		})
		if !ok {
			return errors.New("dispatch not found in the media layer")
		}
		parts, err := s.media.ListParticipants(ctx, room)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(parts, func(p realtime.Participant) bool { return p.Agent && p.Identity == req.AgentIdentity }) {
			return errors.New("claiming identity is not an agent participant of the room")
		}
		return nil
	}
	lease, err := s.svc.ClaimDispatch(r.Context(), req.SessionID, req.ClaimNonce, req.JobID, req.AgentIdentity, verify)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.tier1.Build(r.Context(), req.SessionID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"lease_id": lease.ID, "grant": lease.Grant, "fencing_token": lease.FencingToken,
		"expires_at_unix_ms": lease.ExpiresAt.UnixMilli(), "renew_interval_ms": s.cfg.LeaseRenewInterval.Milliseconds(), "context": c,
	})
}

func (s *Server) tier1Renew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	expires, err := s.svc.RenewLease(r.Context(), p.sessionID, *p.lease)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.tier1.Build(r.Context(), p.sessionID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"expires_at_unix_ms": expires.UnixMilli(), "context": c})
}

func (s *Server) tier1Release(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	if err := s.svc.ReleaseLease(r.Context(), p.sessionID, *p.lease); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) tier1Context(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	s.writeTier1Context(w, r, http.StatusOK, p.sessionID)
}

func (s *Server) tier1Segment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	var sub interview.Submission
	if err := httpx.DecodeJSON(w, r, &sub, maxInternalBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	res, err := s.svc.IngestSegment(r.Context(), actorFor(r.Context(), p), *p.lease, p.sessionID, sub)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, res)
}

func (s *Server) tier1PromptStarted(w http.ResponseWriter, r *http.Request) {
	agentOp(s, w, r, func(p principal, b *struct {
		ActivationID string `json:"activation_id"`
		AtMs         int64  `json:"at_ms"`
	}) error {
		return s.svc.PromptStarted(r.Context(), actorFor(r.Context(), p), *p.lease, p.sessionID, b.ActivationID, b.AtMs)
	})
}

func (s *Server) tier1CalibrationComplete(w http.ResponseWriter, r *http.Request) {
	agentOp(s, w, r, func(p principal, b *versionBody) error {
		return s.svc.CalibrationComplete(r.Context(), actorFor(r.Context(), p), p.lease, p.sessionID, b.StateVersion)
	})
}

func (s *Server) tier1AnswerComplete(w http.ResponseWriter, r *http.Request) {
	agentOp(s, w, r, func(p principal, b *struct {
		StateVersion       int64    `json:"state_version"`
		ActivationID       string   `json:"activation_id"`
		FlushedSegmentKeys []string `json:"flushed_segment_keys"`
		Cause              string   `json:"cause"`
	}) error {
		if len(b.FlushedSegmentKeys) > 500 {
			return &interview.ValidationError{Msg: "too many flushed segment keys"}
		}
		return s.svc.AgentAnswerComplete(r.Context(), actorFor(r.Context(), p), *p.lease, p.sessionID, b.StateVersion, b.ActivationID, b.FlushedSegmentKeys, b.Cause)
	})
}

func (s *Server) tier1Pause(w http.ResponseWriter, r *http.Request) {
	agentOp(s, w, r, func(p principal, b *struct {
		StateVersion int64  `json:"state_version"`
		Reason       string `json:"reason"`
	}) error {
		if b.Reason != "provider_failure" && b.Reason != "audio_unavailable" {
			return &interview.ValidationError{Msg: "reason must be provider_failure or audio_unavailable"}
		}
		v := b.StateVersion
		return s.svc.Pause(r.Context(), actorFor(r.Context(), p), p.lease, p.sessionID, &v, b.Reason)
	})
}

var telemetryMetrics = map[string]bool{"voice_ttfb_ms": true, "voice_ttfa_ms": true, "voice_end_of_turn_ms": true, "voice_full_response_ms": true, "barge_in_stop_latency_ms": true}

func (s *Server) tier1Telemetry(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	var body struct {
		Metrics []struct {
			Name    string  `json:"name"`
			ValueMs float64 `json:"value_ms"`
		} `json:"metrics"`
		PolicyFlags []struct {
			Flag         string  `json:"flag"`
			ActivationID *string `json:"activation_id"`
		} `json:"policy_flags"`
	}
	if err := httpx.DecodeJSON(w, r, &body, maxInternalBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if len(body.Metrics) > 200 || len(body.PolicyFlags) > 50 {
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "too many entries")
		return
	}
	for _, m := range body.Metrics {
		if !telemetryMetrics[m.Name] || m.ValueMs < 0 || m.ValueMs > 600000 {
			httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "unknown metric or value out of range")
			return
		}
	}
	for _, m := range body.Metrics {
		s.metrics.voiceLatency.WithLabelValues(m.Name).Observe(m.ValueMs)
	}
	for _, f := range body.PolicyFlags {
		if err := s.svc.RecordPolicyFlag(r.Context(), *p.lease, p.sessionID, f.Flag, f.ActivationID); err != nil {
			s.writeError(w, r, err)
			return
		}
		s.metrics.policyFlags.WithLabelValues(f.Flag).Inc()
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) tier1Usage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agent(w, r)
	if !ok {
		return
	}
	var u interview.UsageReport
	if err := httpx.DecodeJSON(w, r, &u, maxPublicBody); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if err := s.svc.RecordUsage(r.Context(), *p.lease, p.sessionID, u); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// webhookMaxAge drops replayed or delayed events (ADR-016).
const webhookMaxAge = 5 * time.Minute

// webhookMaxBytes bounds the body read before the signature is checked: the verifier reads the
// whole body to compare its digest with the signed one. LiveKit events carry at most a room's and
// a participant's metadata and attributes (64 KB each by default), so 1 MiB leaves ample room.
const webhookMaxBytes = 1 << 20

func (s *Server) livekitWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, webhookMaxBytes)
	ev, err := s.media.ReceiveWebhook(r)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.metrics.webhooks.WithLabelValues("unknown", "too_large").Inc()
			httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
			return
		}
		s.metrics.webhooks.WithLabelValues("unknown", "rejected").Inc()
		unauthorized(w, r)
		return
	}
	outcome := s.handleWebhook(r.Context(), ev)
	s.metrics.webhooks.WithLabelValues(ev.Event, outcome).Inc()
	if outcome == "error" || outcome == "in_flight" {
		// LiveKit retries 5xx responses; the event stays unprocessed until a delivery succeeds.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleWebhook processes a verified event at least once: a delivery is a duplicate only when an
// earlier delivery of the same event was processed. Every handler re-checks the current state, so
// processing an event again (after a failure, or two deliveries in flight) is harmless.
func (s *Server) handleWebhook(ctx context.Context, ev *realtime.Webhook) string {
	if ev.ID == "" {
		return "ignored"
	}
	if age := s.now().Sub(time.Unix(ev.CreatedAt, 0)); ev.CreatedAt == 0 || age > webhookMaxAge {
		return "stale"
	}
	var processed bool
	err := s.pool.QueryRow(ctx, `WITH ins AS (
			INSERT INTO ati.webhook_events (event_id, event_type, created_unix) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING RETURNING false AS processed)
		SELECT processed FROM ins UNION ALL SELECT processed_at IS NOT NULL FROM ati.webhook_events WHERE event_id = $1 LIMIT 1`,
		ev.ID, ev.Event, ev.CreatedAt).Scan(&processed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "in_flight" // a concurrent delivery inserted the event after this statement began
	}
	if err != nil {
		s.log.Warn("webhook dedupe failed", slog.Any("error", err))
		return "error"
	}
	if processed {
		return "duplicate"
	}
	outcome := s.processWebhook(ctx, ev)
	if outcome == "error" {
		return outcome
	}
	if _, err := s.pool.Exec(ctx, `UPDATE ati.webhook_events SET processed_at = $2 WHERE event_id = $1 AND processed_at IS NULL`, ev.ID, s.now().UTC()); err != nil {
		s.log.Warn("webhook completion not recorded", slog.Any("error", err))
		return "error"
	}
	return outcome
}

func (s *Server) processWebhook(ctx context.Context, ev *realtime.Webhook) string {
	switch ev.Event {
	case "egress_started", "egress_updated", "egress_ended":
		if s.rec == nil {
			return "ignored"
		}
		if err := s.rec.OnEgressEvent(ctx, ev); err != nil {
			s.log.Warn("egress webhook failed", slog.Any("error", err))
			return "error"
		}
		return "processed"
	}
	if ev.Room == "" {
		return "ignored"
	}
	sessionID, err := s.svc.SessionForRoom(ctx, ev.Room)
	if errors.Is(err, interview.ErrNotFound) {
		return "unknown_room"
	}
	if err != nil {
		return "error"
	}
	system := interview.Actor{Type: flow.ActorSystem, RequestID: "webhook:" + ev.ID}
	switch ev.Event {
	case "participant_left":
		parts, err := s.media.ListParticipants(ctx, ev.Room)
		if err != nil {
			return "error"
		}
		if slices.ContainsFunc(parts, func(p realtime.Participant) bool { return p.Identity == ev.ParticipantIdentity }) {
			return "still_present" // rejoined before the event was processed
		}
		if ev.ParticipantIdentity == realtime.CandidateIdentity {
			err := s.svc.Pause(ctx, system, nil, sessionID, nil, "network")
			var refused *interview.TransitionRefusedError
			if err != nil && !errors.As(err, &refused) {
				s.log.Warn("pause on disconnect failed", slog.String("session_id", sessionID), slog.Any("error", err))
				return "error"
			}
			return "paused"
		}
		if ev.ParticipantAgent {
			if err := s.svc.AgentLeft(ctx, sessionID, ev.ParticipantIdentity); err != nil {
				var refused *interview.TransitionRefusedError
				if !errors.As(err, &refused) {
					return "error"
				}
			}
			return "agent_left"
		}
	case "track_published", "track_unpublished":
		// Recording follows the candidate's microphone: it can start only once the track exists, and
		// it stops when the track goes away (ADR-017). The recorder re-checks every precondition.
		if s.rec != nil && ev.ParticipantIdentity == realtime.CandidateIdentity && ev.TrackSource == "microphone" {
			if ev.Event == "track_published" {
				s.rec.Start(ctx, sessionID)
			} else {
				s.rec.Stop(ctx, sessionID)
			}
			return "recording_" + strings.TrimPrefix(ev.Event, "track_")
		}
	case "participant_joined":
		unexpected := ev.ParticipantIdentity != realtime.CandidateIdentity && !ev.ParticipantAgent
		if ev.ParticipantAgent {
			// While an agent holds the lease no other is dispatched, so a second agent participant
			// (for example one minted with a leaked API key to listen in) is removed too.
			holder, err := s.svc.CurrentLeaseHolder(ctx, sessionID)
			if err != nil {
				return "error"
			}
			unexpected = holder != "" && holder != ev.ParticipantIdentity
		}
		if unexpected {
			// Only tokens minted by the control plane can join; anything else indicates key misuse.
			_ = s.media.RemoveParticipant(ctx, ev.Room, ev.ParticipantIdentity)
			s.svc.AuditDenied(ctx, sessionID, system, "media.unexpected_participant", fmt.Errorf("removed participant %q", ev.ParticipantIdentity))
			return "unexpected_removed"
		}
	}
	return "processed"
}

// RunSweepers runs the outbox relay and the deadline, lease, missing-agent, pause-expiry and
// recording sweepers until ctx ends. A pass that fails for some sessions still counts the ones it
// processed.
func (s *Server) RunSweepers(ctx context.Context) {
	fast := time.NewTicker(time.Second)
	slow := time.NewTicker(30 * time.Second)
	defer fast.Stop()
	defer slow.Stop()
	sweep := func(kind string, run func(context.Context) (int, error)) {
		n, err := run(ctx)
		s.metrics.Sweep(kind, n)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("sweeper", slog.String("kind", kind), slog.Any("error", err))
		}
	}
	if s.rec != nil {
		sweep("recording_reconciled", s.rec.Reconcile) // starts interrupted by the last stop
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			if s.relay != nil {
				sweep("outbox_published", s.relay.RunOnce)
			}
			sweep("completion_deadline", s.svc.SweepCompletionDeadlines)
			sweep("lease_expired_pause", s.svc.ExpireLeases)
			sweep("agent_missing_pause", s.svc.SweepMissingAgents)
		case <-slow.C:
			sweep("pause_expired", s.svc.ExpirePausedSessions)
			if s.rec != nil {
				sweep("recording_reconciled", s.rec.Reconcile)
			}
		}
	}
}
