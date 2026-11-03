package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
)

// Metrics holds the control-plane Prometheus collectors (BLUEPRINT section 16). Labels never
// carry personal data or content: routes are patterns, not raw paths.
type Metrics struct {
	reg          *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	transitions  *prometheus.CounterVec
	voiceLatency *prometheus.HistogramVec
	policyFlags  *prometheus.CounterVec
	webhooks     *prometheus.CounterVec
	sweeps       *prometheus.CounterVec
	completions  prometheus.Counter
	cancels      prometheus.Counter
}

// NewMetrics registers the collectors on a fresh registry.
func NewMetrics() *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ati_http_requests_total", Help: "HTTP requests by listener, route pattern, method and status."}, []string{"listener", "route", "method", "status"})
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ati_http_request_duration_seconds", Help: "HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"listener", "route"})
	m.transitions = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ati_state_transitions_total", Help: "Committed state-machine transitions."}, []string{"transition", "actor"})
	m.voiceLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ati_voice_latency_ms", Help: "Agent-reported voice latency metrics (TTFB, TTFA, end-of-turn, full response, barge-in stop).",
		Buckets: []float64{25, 50, 100, 200, 300, 500, 800, 1200, 2000, 5000}}, []string{"metric"})
	m.policyFlags = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ati_tier1_policy_flags_total", Help: "Tier-1 output-monitor flags (review triggers)."}, []string{"flag"})
	m.webhooks = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ati_livekit_webhooks_total", Help: "LiveKit webhooks by event and outcome."}, []string{"event", "outcome"})
	m.sweeps = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ati_sweeper_actions_total", Help: "Background sweeper actions."}, []string{"kind"})
	m.completions = prometheus.NewCounter(prometheus.CounterOpts{Name: "ati_interviews_completed_total", Help: "Interviews that reached INTERVIEW_COMPLETED."})
	m.cancels = prometheus.NewCounter(prometheus.CounterOpts{Name: "ati_interviews_cancelled_total", Help: "Interviews that ended CANCELLED."})
	m.reg.MustRegister(m.httpRequests, m.httpDuration, m.transitions, m.voiceLatency, m.policyFlags, m.webhooks, m.sweeps, m.completions, m.cancels,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Handler serves the registry.
func (m *Metrics) Handler() http.Handler { return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}) }

// Observer returns an httpx observer for a listener.
func (m *Metrics) Observer(listener string) func(route, method string, status int, elapsed time.Duration) {
	return func(route, method string, status int, elapsed time.Duration) {
		m.httpRequests.WithLabelValues(listener, route, method, strconv.Itoa(status)).Inc()
		m.httpDuration.WithLabelValues(listener, route).Observe(elapsed.Seconds())
	}
}

// Transition records a committed transition.
func (m *Metrics) Transition(id string, actor flow.Actor) {
	m.transitions.WithLabelValues(id, string(actor)).Inc()
	switch id {
	case "T11":
		m.completions.Inc()
	case "T03", "T21", "T22", "T23", "T24", "T25", "T26":
		m.cancels.Inc()
	}
}

// Sweep records a sweeper action.
func (m *Metrics) Sweep(kind string, n int) {
	if n > 0 {
		m.sweeps.WithLabelValues(kind).Add(float64(n))
	}
}
