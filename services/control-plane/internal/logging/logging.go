// Package logging builds the structured logger. Redaction is applied by the handler itself so a
// careless call site cannot leak credentials, tokens, transcript text or audio (MVP criterion 10,
// BLUEPRINT section 16). Request bodies are never logged.
package logging

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted replaces any value removed by the handler.
const Redacted = "[REDACTED]"

// sensitiveKey matches attribute keys whose values must never be logged.
var sensitiveKey = regexp.MustCompile(`(?i)(authorization|cookie|token|secret|password|passwd|grant|nonce|api[_-]?key|` +
	`credential|transcript|text|quote|utterance|audio|email|phone|candidate_ref|invitation|set-cookie|body)`)

// sensitiveValue matches string values that look like secrets regardless of their key:
// JWTs, bearer headers, long base64url/hex strings (tokens, grants, hashes).
var sensitiveValue = regexp.MustCompile(`(?i)(^bearer\s|eyJ[A-Za-z0-9_-]{8,}\.|^[A-Za-z0-9_-]{32,}$|^[0-9a-f]{40,}$)`)

// New returns a JSON logger with mandatory redaction.
func New(w io.Writer, level string, service string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level), ReplaceAttr: redact})
	return slog.New(h).With(slog.String("service", service))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func redact(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey || a.Key == slog.SourceKey) {
		return a
	}
	if sensitiveKey.MatchString(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	for _, g := range groups {
		if sensitiveKey.MatchString(g) {
			return slog.String(a.Key, Redacted)
		}
	}
	if a.Value.Kind() == slog.KindString && sensitiveValue.MatchString(a.Value.String()) {
		return slog.String(a.Key, Redacted)
	}
	return a
}
