package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/logging"
)

// A client that went away (a candidate closing the tab while waiting for a sign-in slot) is not a
// server error: it is answered 499 and not logged as a failure. A cancellation the client did not
// cause still is one.
func TestAClientThatWentAwayIsNotAServerError(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{log: logging.New(&logs, "info", "control-plane-test")}
	gone, leave := context.WithCancel(context.Background())
	leave()
	w := httptest.NewRecorder()
	s.writeError(w, httptest.NewRequest(http.MethodPost, "/api/v1/candidate/session", nil).WithContext(gone),
		fmt.Errorf("waiting for a verification slot: %w", context.Canceled))
	if w.Code != 499 || !strings.Contains(w.Body.String(), `"code":"client_closed_request"`) || logs.Len() != 0 {
		t.Fatalf("a client that went away: %d %s, logged %q", w.Code, w.Body.String(), logs.String())
	}
	w = httptest.NewRecorder()
	s.writeError(w, httptest.NewRequest(http.MethodGet, "/api/v1/interviews/x", nil), context.Canceled)
	if w.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "request failed") {
		t.Fatalf("a cancellation the client did not cause: %d, logged %q", w.Code, logs.String())
	}
}
