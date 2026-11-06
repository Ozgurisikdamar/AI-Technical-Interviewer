// Package httpx holds HTTP plumbing shared by both listeners: RFC 9457 problem responses,
// strict JSON decoding, correlation IDs, security headers, panic recovery, trusted-proxy client
// IPs and the same-origin guard for mutating public requests (ADR-016).
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
)

// StatusClientClosedRequest is nginx's status for a request whose client went away before it was
// answered. It keeps such requests out of the server-error count.
const StatusClientClosedRequest = 499

// Stable problem codes. Clients branch on these, never on titles or details.
const (
	CodeInvalidRequest         = "invalid_request"
	CodeUnauthorized           = "unauthorized"
	CodeForbidden              = "forbidden"
	CodeNotFound               = "not_found"
	CodeStaleStateVersion      = "stale_state_version"
	CodeTransitionNotAllowed   = "transition_not_allowed"
	CodeIdempotencyKeyRequired = "idempotency_key_required"
	CodeIdempotencyKeyReused   = "idempotency_key_reused"
	CodeRateLimited            = "rate_limited"
	CodePayloadTooLarge        = "payload_too_large"
	CodeUnsupportedMediaType   = "unsupported_media_type"
	CodeOriginMismatch         = "origin_mismatch"
	// CodeClientClosedRequest answers a request whose client went away before it was handled.
	CodeClientClosedRequest = "client_closed_request"
	CodeInternal            = "internal_error"
	CodeUnavailable         = "unavailable"
	CodeInvitationInvalid   = "invitation_invalid"
	CodeRetentionMissing    = "retention_policy_missing"
	CodePlanUnavailable     = "plan_unavailable"
	CodeLeaseStale          = "lease_stale"
	CodeSegmentConflict     = "segment_conflict"
	CodeSegmentRejected     = "segment_rejected"
	CodeManifestIncomplete  = "manifest_incomplete"
	CodeNonceInvalid        = "claim_nonce_invalid"
	CodeSignInFailed        = "sign_in_failed"
	CodeConflict            = "conflict"
)

// Problem is an RFC 9457 problem document.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

// WriteProblem writes an application/problem+json response.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{
		Type:      "https://example.invalid/problems/" + code,
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: RequestIDFrom(r.Context()),
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteJSON writes a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeError describes a rejected request body.
type DecodeError struct {
	Status int
	Code   string
	Msg    string
}

func (e *DecodeError) Error() string { return e.Msg }

// DecodeJSON strictly decodes a single JSON object: bounded size, no unknown fields, no trailing data.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return &DecodeError{http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "Content-Type must be application/json"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &DecodeError{http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large"}
		}
		return &DecodeError{http.StatusBadRequest, CodeInvalidRequest, "malformed JSON body: " + sanitize(err.Error())}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &DecodeError{http.StatusBadRequest, CodeInvalidRequest, "request body must contain a single JSON object"}
	}
	return nil
}

// sanitize keeps decoder messages but never echoes request content back.
func sanitize(msg string) string {
	if i := strings.Index(msg, "json: unknown field"); i >= 0 {
		return msg[i:]
	}
	if len(msg) > 120 {
		return msg[:120]
	}
	return msg
}

// WriteDecodeError maps a DecodeJSON error to a problem response.
func WriteDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var de *DecodeError
	if errors.As(err, &de) {
		WriteProblem(w, r, de.Status, de.Code, de.Msg)
		return
	}
	WriteProblem(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid request")
}

type ctxKey int

const requestIDKey ctxKey = 1

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)

// RequestIDFrom returns the correlation ID of the request.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// WithRequestID echoes a well-formed X-Request-ID or generates one.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			id = ids.New(ids.Request)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// WithAPIHeaders sets defensive headers for JSON APIs and the environment marker (ADR-020).
func WithAPIHeaders(env string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-ATI-Environment", env)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Observer receives per-request outcomes (for metrics).
type Observer func(route string, method string, status int, elapsed time.Duration)

// WithRecoveryAndAccessLog recovers panics as 500 problems and logs method, route pattern, status
// and duration only (never bodies, query strings or headers).
func WithRecoveryAndAccessLog(log *slog.Logger, observe Observer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic serving request", slog.String("request_id", RequestIDFrom(r.Context())), slog.String("route", r.Pattern), slog.String("panic", fmt.Sprint(v)))
				if rec.status == 0 {
					WriteProblem(rec, r, http.StatusInternalServerError, CodeInternal, "internal error")
				}
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			elapsed := time.Since(start)
			if observe != nil {
				observe(route, r.Method, rec.status, elapsed)
			}
			log.Info("http", slog.String("request_id", RequestIDFrom(r.Context())), slog.String("route", route),
				slog.Int("status", rec.status), slog.Int64("duration_ms", elapsed.Milliseconds()))
		}()
		next.ServeHTTP(rec, r)
	})
}

// ClientIP returns the client address, honouring X-Forwarded-For only from trusted proxies.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	if !isTrusted(peer, trusted) {
		return peer
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer
		}
		a = a.Unmap()
		if !isTrusted(a, trusted) {
			return a
		}
	}
	return peer
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// WithSameOrigin rejects mutating requests whose Origin or Sec-Fetch-Site does not match the
// configured public origin (CSRF defence in depth on top of SameSite=Strict cookies).
// Requests carrying a staff bearer token are exempt: bearer tokens are not ambient credentials.
func WithSameOrigin(origin *url.URL, next http.Handler) http.Handler {
	want := origin.Scheme + "://" + origin.Host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && r.Header.Get("Cookie") == "" {
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			WriteProblem(w, r, http.StatusForbidden, CodeOriginMismatch, "cross-site request refused")
			return
		}
		if o := r.Header.Get("Origin"); o != want {
			WriteProblem(w, r, http.StatusForbidden, CodeOriginMismatch, "origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}
