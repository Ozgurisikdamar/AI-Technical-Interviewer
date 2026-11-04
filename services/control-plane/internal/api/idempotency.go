package api

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/httpx"
)

var idemKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)

// staleClaim is how long a claim without an outcome blocks its key: longer than any request runs,
// so an older claim belongs to a request that never finished (the process stopped mid-request).
const staleClaim = 5 * time.Minute

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	replay []byte // stored instead of body when set (see replayBody)
}

// replayBody sets what an idempotent replay returns instead of the response itself. Credentials
// such as an invitation token are returned once and never stored (THREAT_MODEL_ADDENDUM_PHASE1).
func replayBody(w http.ResponseWriter, v any) {
	if rec, ok := w.(*recorder); ok {
		rec.replay, _ = json.Marshal(v)
	}
}

// requireIdempotencyKey checks the header on endpoints whose responses are never replayed from
// storage because they hold a fresh credential, and whose repetition is safe.
func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	if !idemKeyPattern.MatchString(r.Header.Get("Idempotency-Key")) {
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeIdempotencyKeyRequired, "a valid Idempotency-Key header is required")
		return false
	}
	return true
}

func newRecorder() *recorder                    { return &recorder{header: http.Header{}} }
func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *recorder) flush(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	if r.status == 0 {
		r.status = http.StatusOK
	}
	w.WriteHeader(r.status)
	_, _ = w.Write(r.body.Bytes())
}

type storedResponse struct {
	ContentType string          `json:"content_type"`
	Body        json.RawMessage `json:"body"`
}

// readBody reads and restores a bounded request body.
func readBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	b, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
		return nil, false
	case err != nil: // the client stopped sending (disconnect or read timeout)
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "request body could not be read")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, true
}

// idempotent executes run at most once per (principal, Idempotency-Key). Replays return the
// stored response; reuse with a different request is refused; 5xx outcomes may be retried.
// Response headers such as Set-Cookie are never stored (no secrets at rest).
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, principalKey string, run func(w http.ResponseWriter, r *http.Request)) {
	key := r.Header.Get("Idempotency-Key")
	if !idemKeyPattern.MatchString(key) {
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeIdempotencyKeyRequired, "a valid Idempotency-Key header is required")
		return
	}
	body, ok := readBody(w, r, maxPublicBody)
	if !ok {
		return
	}
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
	hash := hex.EncodeToString(sum[:])
	ctx := r.Context()
	tag, err := s.pool.Exec(ctx, `INSERT INTO ati.idempotency_records (principal, idem_key, route, request_sha256) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		principalKey, key, r.Pattern, hash)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		var prevHash string
		var status *int
		var resp []byte
		if err := s.pool.QueryRow(ctx, `SELECT request_sha256, status, response FROM ati.idempotency_records WHERE principal = $1 AND idem_key = $2`, principalKey, key).
			Scan(&prevHash, &status, &resp); err != nil {
			s.writeError(w, r, err)
			return
		}
		switch {
		case prevHash != hash:
			httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeIdempotencyKeyReused, "Idempotency-Key was already used for a different request")
			return
		case status == nil:
			now := s.now().UTC()
			t, err := s.pool.Exec(ctx, `UPDATE ati.idempotency_records SET claimed_at = $3 WHERE principal = $1 AND idem_key = $2 AND status IS NULL AND claimed_at <= $4`,
				principalKey, key, now, now.Add(-staleClaim))
			if err != nil || t.RowsAffected() == 0 {
				httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeConflict, "an identical request is still in progress")
				return
			}
			// The earlier request never finished: this one takes over the claim.
		case *status == 0:
			// Previous attempt failed with a server error: allow exactly one retry to claim it.
			t, err := s.pool.Exec(ctx, `UPDATE ati.idempotency_records SET status = NULL WHERE principal = $1 AND idem_key = $2 AND status = 0`, principalKey, key)
			if err != nil || t.RowsAffected() == 0 {
				httpx.WriteProblem(w, r, http.StatusConflict, httpx.CodeConflict, "an identical request is still in progress")
				return
			}
		default:
			var sr storedResponse
			_ = json.Unmarshal(resp, &sr)
			w.Header().Set("Content-Type", sr.ContentType)
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(*status)
			_, _ = w.Write(sr.Body)
			return
		}
	}
	rec := newRecorder()
	run(rec, r)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	// The outcome is recorded even when the client has gone away meanwhile: otherwise the claim
	// would block the key until it goes stale.
	book, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if status >= 500 {
		_, _ = s.pool.Exec(book, `UPDATE ati.idempotency_records SET status = 0 WHERE principal = $1 AND idem_key = $2`, principalKey, key)
	} else {
		body := rec.body.Bytes()
		if rec.replay != nil {
			body = rec.replay
		}
		stored, _ := json.Marshal(storedResponse{ContentType: rec.header.Get("Content-Type"), Body: json.RawMessage(nonEmptyJSON(body))})
		_, _ = s.pool.Exec(book, `UPDATE ati.idempotency_records SET status = $3, response = $4 WHERE principal = $1 AND idem_key = $2`, principalKey, key, status, stored)
	}
	rec.flush(w)
}

func nonEmptyJSON(b []byte) []byte {
	if len(bytes.TrimSpace(b)) == 0 || !json.Valid(b) {
		return []byte("null")
	}
	return b
}

// ipLimiter rate-limits anonymous endpoints per client. IPv4 clients are keyed by address and
// IPv6 clients by their /64, which a single subscriber typically controls in full; the table is
// bounded (least recently seen keys are evicted first), so many distinct addresses cannot grow it.
type ipLimiter struct {
	mu      sync.Mutex
	perMin  int
	max     int
	entries map[netip.Addr]*list.Element
	order   *list.List // front: most recently seen
}

type limiterEntry struct {
	key netip.Addr
	lim *rate.Limiter
}

// limiterCapacity bounds the table: at the default rate an entry is a few hundred bytes.
const limiterCapacity = 10000

func newIPLimiter(perMinute int) *ipLimiter {
	return &ipLimiter{perMin: perMinute, max: limiterCapacity, entries: map[netip.Addr]*list.Element{}, order: list.New()}
}

// limiterKey is the address, or its /64 for IPv6.
func limiterKey(a netip.Addr) netip.Addr {
	a = a.Unmap()
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.Addr()
		}
	}
	return a
}

func (l *ipLimiter) allow(a netip.Addr, now time.Time) bool {
	key := limiterKey(a)
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.entries[key]; ok {
		l.order.MoveToFront(el)
		return el.Value.(*limiterEntry).lim.AllowN(now, 1)
	}
	for l.order.Len() >= l.max {
		oldest := l.order.Back()
		delete(l.entries, oldest.Value.(*limiterEntry).key)
		l.order.Remove(oldest)
	}
	e := &limiterEntry{key: key, lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.perMin)), l.perMin)}
	l.entries[key] = l.order.PushFront(e)
	return e.lim.AllowN(now, 1)
}
