package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestProblemFormat(t *testing.T) {
	h := WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusConflict, CodeStaleStateVersion, "state changed")
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Request-ID", "client-req-12345")
	h.ServeHTTP(rr, req)
	if rr.Code != 409 || rr.Header().Get("Content-Type") != "application/problem+json" || rr.Header().Get("X-Request-ID") != "client-req-12345" {
		t.Fatalf("unexpected response: %d %v", rr.Code, rr.Header())
	}
	var p Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil || p.Code != CodeStaleStateVersion || p.RequestID != "client-req-12345" || p.Status != 409 {
		t.Fatalf("bad problem: %+v %v", p, err)
	}
}

func TestMalformedRequestIDIsReplaced(t *testing.T) {
	var seen string
	h := WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces\n")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(seen, "req_") {
		t.Fatalf("expected generated id, got %q", seen)
	}
}

type body struct {
	StateVersion int `json:"state_version"`
}

func decode(t *testing.T, ct, payload string, max int64) (int, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	req.Header.Set("Content-Type", ct)
	var b body
	if err := DecodeJSON(rr, req, &b, max); err != nil {
		WriteDecodeError(rr, req, err)
		var p Problem
		_ = json.Unmarshal(rr.Body.Bytes(), &p)
		return rr.Code, p.Code
	}
	return 200, ""
}

func TestStrictDecoding(t *testing.T) {
	cases := []struct {
		name, ct, body string
		max            int64
		status         int
		code           string
	}{
		{"ok", "application/json", `{"state_version":3}`, 1024, 200, ""},
		{"unknown field", "application/json", `{"state_version":3,"score":100}`, 1024, 400, CodeInvalidRequest},
		{"trailing object", "application/json", `{"state_version":3}{"x":1}`, 1024, 400, CodeInvalidRequest},
		{"wrong type", "application/json", `{"state_version":"3"}`, 1024, 400, CodeInvalidRequest},
		{"too large", "application/json", `{"state_version":` + strings.Repeat("1", 200) + `}`, 64, 413, CodePayloadTooLarge},
		{"form encoded", "application/x-www-form-urlencoded", `state_version=3`, 1024, 415, CodeUnsupportedMediaType},
	}
	for _, c := range cases {
		status, code := decode(t, c.ct, c.body, c.max)
		if status != c.status || code != c.code {
			t.Errorf("%s: got %d %q, want %d %q", c.name, status, code, c.status, c.code)
		}
	}
}

func TestDecodeErrorsNeverEchoContent(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"state_version": SENTINEL-INJECTED-TEXT}`))
	req.Header.Set("Content-Type", "application/json")
	var b body
	WriteDecodeError(rr, req, DecodeJSON(rr, req, &b, 1024))
	if strings.Contains(rr.Body.String(), "SENTINEL") {
		t.Fatalf("decoder echoed request content: %s", rr.Body.String())
	}
}

func TestRecoveryAndAccessLogNeverLogBodies(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /boom/{id}", func(w http.ResponseWriter, r *http.Request) { panic("kaboom") })
	h := WithRequestID(WithRecoveryAndAccessLog(log, nil, mux))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/boom/1?token=SENTINEL-QUERY", strings.NewReader("SENTINEL-BODY")))
	if rr.Code != 500 {
		t.Fatalf("want 500, got %d", rr.Code)
	}
	if strings.Contains(logs.String(), "SENTINEL") {
		t.Fatalf("query or body leaked into logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "POST /boom/{id}") {
		t.Fatalf("route pattern should be logged: %s", logs.String())
	}
}

func TestClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 127.0.0.1")
	if got := ClientIP(req, trusted); got.String() != "198.51.100.7" {
		t.Fatalf("trusted proxy: got %v", got)
	}
	req.RemoteAddr = "203.0.113.9:4444"
	if got := ClientIP(req, trusted); got.String() != "203.0.113.9" {
		t.Fatalf("untrusted peer must not be able to spoof X-Forwarded-For: got %v", got)
	}
}

func TestSameOriginGuard(t *testing.T) {
	origin, _ := url.Parse("http://localhost:3000")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := WithSameOrigin(origin, ok)
	try := func(method string, headers map[string]string) int {
		req := httptest.NewRequest(method, "/api/v1/interviews/x/pause", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if c := try(http.MethodPost, map[string]string{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "same-origin", "Cookie": "ati_cs=x"}); c != 204 {
		t.Errorf("same origin refused: %d", c)
	}
	if c := try(http.MethodPost, map[string]string{"Origin": "https://evil.example", "Cookie": "ati_cs=x"}); c != 403 {
		t.Errorf("foreign origin allowed: %d", c)
	}
	if c := try(http.MethodPost, map[string]string{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "cross-site", "Cookie": "ati_cs=x"}); c != 403 {
		t.Errorf("cross-site fetch allowed: %d", c)
	}
	if c := try(http.MethodPost, map[string]string{"Cookie": "ati_cs=x"}); c != 403 {
		t.Errorf("missing Origin allowed: %d", c)
	}
	if c := try(http.MethodPost, map[string]string{"Authorization": "Bearer staff"}); c != 204 {
		t.Errorf("bearer-only API call refused: %d", c)
	}
	if c := try(http.MethodGet, nil); c != 204 {
		t.Errorf("safe method refused: %d", c)
	}
}
