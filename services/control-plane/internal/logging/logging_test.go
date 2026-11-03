package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

const (
	sentinelSecret     = "test-only-sentinel-secret-7f3a9"
	sentinelTranscript = "SENTINEL-TRANSCRIPT-PostgreSQL-uses-MVCC"
	sentinelEmail      = "candidate-sentinel@example.com"
)

func TestSensitiveKeysAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "debug", "test")
	log.Info("request",
		slog.String("authorization", "Bearer "+sentinelSecret),
		slog.String("X-ATI-Agent-Grant", sentinelSecret),
		slog.String("livekit_api_secret", sentinelSecret),
		slog.String("text", sentinelTranscript),
		slog.String("candidate_ref", sentinelEmail),
		slog.String("email", sentinelEmail),
		slog.Group("segment", slog.String("transcript", sentinelTranscript)),
		slog.Group("invitation", slog.String("value", sentinelSecret)),
		slog.String("session_id", "ses_0123456789abcdefABCDEF"),
	)
	out := buf.String()
	for _, s := range []string{sentinelSecret, sentinelTranscript, sentinelEmail} {
		if strings.Contains(out, s) {
			t.Fatalf("sentinel %q leaked into log: %s", s, out)
		}
	}
	if !strings.Contains(out, "ses_0123456789abcdefABCDEF") {
		t.Fatalf("opaque identifiers must stay visible for correlation: %s", out)
	}
}

func TestSecretLookingValuesAreRedactedUnderInnocentKeys(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "test")
	// Assembled at runtime so the repository never contains a literal token.
	jwt := strings.Join([]string{"eyJ" + "hbGciOiJIUzI1NiJ9", "eyJ" + "zdWIiOiJ4In0", "c2lnbmF0dXJlc2lnbmF0dXJl"}, ".")
	grant := "Zx8kQ2v9L1mN4pR7sT0uW3yA6bC9dE2fG5hJ8kL1mN4"
	hash := strings.Repeat("ab", 32)
	log.Info("x", slog.String("detail", jwt), slog.String("value", grant), slog.String("ref", hash))
	out := buf.String()
	for _, s := range []string{jwt, grant, hash} {
		if strings.Contains(out, s) {
			t.Fatalf("secret-looking value leaked: %s", out)
		}
	}
}

func TestErrorsAndLevelsStillUsable(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "warn", "test")
	log.Info("dropped")
	log.Warn("kept", slog.Any("error", errors.New("database unavailable")))
	out := buf.String()
	if strings.Contains(out, "dropped") || !strings.Contains(out, "database unavailable") || !strings.Contains(out, `"service":"test"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
