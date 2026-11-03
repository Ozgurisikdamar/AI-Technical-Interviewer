package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func devEnv() map[string]string {
	return map[string]string{
		"APP_ENV":                              "dev",
		"ATI_PUBLIC_ORIGIN":                    "http://localhost:3000",
		"ATI_DATABASE_URL":                     "postgres://ati_control_plane@127.0.0.1:5432/ati",
		"ATI_TIER1_CTX_DATABASE_URL":           "postgres://ati_tier1_ctx@127.0.0.1:5432/ati",
		"ATI_LIVEKIT_URL":                      "ws://localhost:7880",
		"ATI_LIVEKIT_API_KEY":                  "devapikey",
		"ATI_LIVEKIT_API_SECRET":               "ati-dev-livekit-api-secret-0123456789",
		"ATI_LIVEKIT_WEBHOOK_API_KEY":          "devwebhookkey",
		"ATI_LIVEKIT_WEBHOOK_API_SECRET":       "ati-dev-livekit-webhook-secret-0123456789",
		"ATI_AGENT_SERVICE_TOKEN_SHA256":       strings.Repeat("ab", 32),
		"ATI_RETENTION_TRANSCRIPT_DAYS":        "30",
		"ATI_RETENTION_SESSION_EVENTS_DAYS":    "30",
		"ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS": "30",
	}
}

func getter(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func with(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestValidDevConfig(t *testing.T) {
	c, err := Load(getter(devEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.LiveKitAPIURL != "http://localhost:7880" || !c.IsDevLike() || c.SecureCookies() || !c.RetentionConfigured() {
		t.Fatalf("unexpected config: %+v", c)
	}
	if len(c.TrustedProxies) != 2 {
		t.Fatalf("dev should trust loopback proxies by default, got %v", c.TrustedProxies)
	}
}

func TestRefusesUnknownOrMissingEnvironment(t *testing.T) {
	for _, env := range []string{"", "prod", "development", "PRODUCTION"} {
		if _, err := Load(getter(with(devEnv(), "APP_ENV", env))); err == nil || !strings.Contains(err.Error(), "refusing to start") {
			t.Errorf("APP_ENV=%q must be refused, got %v", env, err)
		}
	}
}

func TestReportsAllMissingRequiredSettings(t *testing.T) {
	_, err := Load(getter(map[string]string{"APP_ENV": "test"}))
	if err == nil {
		t.Fatal("empty config accepted")
	}
	for _, key := range []string{"ATI_DATABASE_URL", "ATI_LIVEKIT_API_SECRET", "ATI_AGENT_SERVICE_TOKEN_SHA256", "ATI_PUBLIC_ORIGIN"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should mention %s: %v", key, err)
		}
	}
}

func TestRejectsUnsafeValues(t *testing.T) {
	cases := map[string]map[string]string{
		"webhook key reused": with(devEnv(), "ATI_LIVEKIT_WEBHOOK_API_KEY", "devapikey"),
		"bad agent hash":     with(devEnv(), "ATI_AGENT_SERVICE_TOKEN_SHA256", "not-a-hash"),
		"origin with path":   with(devEnv(), "ATI_PUBLIC_ORIGIN", "http://localhost:3000/app"),
		"http livekit url":   with(devEnv(), "ATI_LIVEKIT_URL", "http://localhost:7880"),
		"renew >= ttl":       with(devEnv(), "ATI_LEASE_RENEW_INTERVAL", "20s"),
		"negative duration":  with(devEnv(), "ATI_PAUSE_TIMEOUT", "-1m"),
		"bad retention":      with(devEnv(), "ATI_RETENTION_TRANSCRIPT_DAYS", "forever"),
		"bad proxy cidr":     with(devEnv(), "ATI_TRUSTED_PROXY_CIDRS", "10.0.0.0/99"),
		"bad boolean":        with(devEnv(), "ATI_RECORDING_ENABLED", "maybe"),
	}
	for name, env := range cases {
		if _, err := Load(getter(env)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRetentionIsNotDefaulted(t *testing.T) {
	env := with(devEnv(), "ATI_RETENTION_TRANSCRIPT_DAYS", "")
	c, err := Load(getter(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.RetentionConfigured() {
		t.Fatal("retention must not be invented when unset")
	}
}

func TestRecordingIsFailClosed(t *testing.T) {
	base := with(devEnv(), "ATI_RECORDING_ENABLED", "true")
	full := with(base,
		"ATI_RETENTION_AUDIO_RECORDING_DAYS", "14",
		"ATI_RECORDING_STORAGE_ENCRYPTION_ATTESTED", "true",
		"ATI_RECORDING_S3_ENDPOINT", "127.0.0.1:9000",
		"ATI_RECORDING_S3_BUCKET", "recordings",
	)
	if _, err := Load(getter(full)); err != nil {
		t.Fatalf("complete recording config refused: %v", err)
	}
	for _, drop := range []string{"ATI_RETENTION_AUDIO_RECORDING_DAYS", "ATI_RECORDING_STORAGE_ENCRYPTION_ATTESTED", "ATI_RECORDING_S3_BUCKET"} {
		if _, err := Load(getter(with(full, drop, ""))); err == nil {
			t.Errorf("recording enabled without %s accepted", drop)
		}
	}
}

func prodEnv(t *testing.T) map[string]string {
	t.Helper()
	gates := filepath.Join(t.TempDir(), "gates.json")
	content := `{"gates":[` +
		`{"id":"evaluation_validity","approved":true,"approver":"A. Reviewer","approved_on":"2027-01-10"},` +
		`{"id":"security","approved":true,"approver":"B. Reviewer","approved_on":"2027-01-10"},` +
		`{"id":"reliability","approved":true,"approver":"C. Reviewer","approved_on":"2027-01-10"},` +
		`{"id":"privacy_legal","approved":true,"approver":"D. Reviewer","approved_on":"2027-01-10"},` +
		`{"id":"human_governance","approved":true,"approver":"E. Reviewer","approved_on":"2027-01-10"}]}`
	if err := os.WriteFile(gates, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return with(devEnv(),
		"APP_ENV", "production",
		"ATI_PUBLIC_ORIGIN", "https://interview.example.com",
		"ATI_LIVEKIT_URL", "wss://media.example.com",
		"ATI_LIVEKIT_API_SECRET", "prod-livekit-secret-from-secret-manager",
		"ATI_LIVEKIT_WEBHOOK_API_SECRET", "prod-webhook-secret-from-secret-manager",
		"ATI_PRODUCTION_GATES_FILE", gates,
	)
}

func TestProductionIsRefusedWhileNoticeIsDraft(t *testing.T) {
	_, err := Load(getter(prodEnv(t)))
	if err == nil || !strings.Contains(err.Error(), "legally approved notice") {
		t.Fatalf("production with a DRAFT notice must be refused, got %v", err)
	}
}

func TestProductionRefusalReasons(t *testing.T) {
	cases := map[string]struct {
		kv   []string
		want string
	}{
		"dev secret":     {[]string{"ATI_LIVEKIT_API_SECRET", DevSecretPrefix + "x"}, "development placeholder"},
		"http origin":    {[]string{"ATI_PUBLIC_ORIGIN", "http://interview.example.com"}, "https ATI_PUBLIC_ORIGIN"},
		"ws livekit":     {[]string{"ATI_LIVEKIT_URL", "ws://media.example.com"}, "wss://"},
		"no retention":   {[]string{"ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS", ""}, "explicit retention"},
		"no gates file":  {[]string{"ATI_PRODUCTION_GATES_FILE", ""}, "human sign-off"},
		"unknown notice": {[]string{"ATI_NOTICE_VERSION", "2099-01-x"}, "not found"},
	}
	for name, c := range cases {
		_, err := Load(getter(with(prodEnv(t), c.kv...)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", name, c.want, err)
		}
	}
}

func TestProductionGatesRequireNamedHumanSignOff(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	partial := write("partial.json", `{"gates":[{"id":"security","approved":true,"approver":"X","approved_on":"2027-01-01"}]}`)
	if err := CheckProductionGates(partial); err == nil || !strings.Contains(err.Error(), "evaluation_validity") {
		t.Fatalf("partial gates accepted: %v", err)
	}
	anonymous := write("anon.json", `{"gates":[`+strings.Repeat(`{"id":"x"},`, 0)+
		`{"id":"evaluation_validity","approved":true,"approver":" ","approved_on":"2027-01-01"},`+
		`{"id":"security","approved":true,"approver":"X","approved_on":"2027-01-01"},`+
		`{"id":"reliability","approved":true,"approver":"X","approved_on":"2027-01-01"},`+
		`{"id":"privacy_legal","approved":true,"approver":"X","approved_on":"2027-01-01"},`+
		`{"id":"human_governance","approved":true,"approver":"X","approved_on":"not-a-date"}]}`)
	err := CheckProductionGates(anonymous)
	if err == nil || !strings.Contains(err.Error(), "evaluation_validity") || !strings.Contains(err.Error(), "human_governance") {
		t.Fatalf("gates without a named approver or valid date accepted: %v", err)
	}
}
