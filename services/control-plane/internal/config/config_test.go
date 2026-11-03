package config

import (
	"fmt"
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

const (
	approvedNoticeID      = "notice_fixture"
	approvedNoticeVersion = "2027-01-approved.1"
)

// withNotices replaces the shipped (draft) notices with an approved fixture for one test, so each
// production refusal below starts from a configuration that would otherwise be accepted.
func withNotices(t *testing.T, basis string) {
	t.Helper()
	orig := notices
	t.Cleanup(func() { notices = orig })
	doc := fmt.Sprintf(`{"notice_id":%q,"version":%q,"status":"approved","purposes":[{"basis":"consent"},{"basis":%q}]}`,
		approvedNoticeID, approvedNoticeVersion, basis)
	notices = func() (map[string][]byte, error) { return map[string][]byte{"fixture.en.json": []byte(doc)}, nil }
}

func approvedProdEnv(t *testing.T) map[string]string {
	t.Helper()
	withNotices(t, "legitimate_interest")
	return with(prodEnv(t), "ATI_NOTICE_ID", approvedNoticeID, "ATI_NOTICE_VERSION", approvedNoticeVersion)
}

func TestApprovedProductionConfigurationIsAccepted(t *testing.T) {
	c, err := Load(getter(approvedProdEnv(t)))
	if err != nil {
		t.Fatalf("a production configuration meeting every ADR-020 gate was refused: %v", err)
	}
	if !c.SecureCookies() || c.IsDevLike() || len(c.TrustedProxies) != 0 {
		t.Fatalf("production defaults: secure=%v devlike=%v proxies=%v", c.SecureCookies(), c.IsDevLike(), c.TrustedProxies)
	}
}

// Each case breaks exactly one gate of an otherwise valid production configuration and must be
// refused for that reason alone.
func TestProductionRefusalReasons(t *testing.T) {
	gates := func(t *testing.T, security string) string {
		p := filepath.Join(t.TempDir(), "gates.json")
		var parts []string
		for _, g := range RequiredGates {
			entry := fmt.Sprintf(`{"id":%q,"approved":true,"approver":"R. Eviewer","approved_on":"2027-01-10"}`, g)
			if g == "security" {
				entry = security
			}
			parts = append(parts, entry)
		}
		if err := os.WriteFile(p, []byte(`{"gates":[`+strings.Join(parts, ",")+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	shipped := notices
	cases := map[string]struct {
		kv      []string
		basis   string
		shipped bool // check against the notices in this repository (all drafts)
		want    string
	}{
		"dev api secret":      {kv: []string{"ATI_LIVEKIT_API_SECRET", DevSecretPrefix + "x"}, want: "ATI_LIVEKIT_API_SECRET is a development placeholder"},
		"dev webhook secret":  {kv: []string{"ATI_LIVEKIT_WEBHOOK_API_SECRET", DevSecretPrefix + "x"}, want: "ATI_LIVEKIT_WEBHOOK_API_SECRET is a development placeholder"},
		"dev s3 secret":       {kv: []string{"ATI_RECORDING_S3_SECRET_KEY", DevSecretPrefix + "x"}, want: "ATI_RECORDING_S3_SECRET_KEY is a development placeholder"},
		"http origin":         {kv: []string{"ATI_PUBLIC_ORIGIN", "http://interview.example.com"}, want: "https ATI_PUBLIC_ORIGIN"},
		"ws livekit":          {kv: []string{"ATI_LIVEKIT_URL", "ws://media.example.com"}, want: "wss://"},
		"no retention":        {kv: []string{"ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS", ""}, want: "explicit retention"},
		"no gates file":       {kv: []string{"ATI_PRODUCTION_GATES_FILE", ""}, want: "human sign-off"},
		"unknown notice":      {kv: []string{"ATI_NOTICE_VERSION", "2099-01-x"}, want: "not found"},
		"draft notice":        {kv: []string{"ATI_NOTICE_ID", "notice_default", "ATI_NOTICE_VERSION", "2026-11-draft.1"}, shipped: true, want: "legally approved notice"},
		"undetermined basis":  {basis: "to_be_determined_by_legal", want: "undetermined lawful basis"},
		"gate not approved":   {kv: []string{"ATI_PRODUCTION_GATES_FILE", "security:false"}, want: "[security]"},
		"gate without signer": {kv: []string{"ATI_PRODUCTION_GATES_FILE", "security:anonymous"}, want: "[security]"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := approvedProdEnv(t)
			if c.basis != "" {
				withNotices(t, c.basis)
			}
			if c.shipped {
				notices = shipped
			}
			kv := append([]string(nil), c.kv...)
			for i := 0; i+1 < len(kv); i += 2 {
				switch kv[i+1] {
				case "security:false":
					kv[i+1] = gates(t, `{"id":"security","approved":false,"approver":"R. Eviewer","approved_on":"2027-01-10"}`)
				case "security:anonymous":
					kv[i+1] = gates(t, `{"id":"security","approved":true,"approver":"","approved_on":"2027-01-10"}`)
				}
			}
			if strings.HasPrefix(name, "dev s3") {
				kv = append(kv, "ATI_RECORDING_ENABLED", "true", "ATI_RETENTION_AUDIO_RECORDING_DAYS", "14", "ATI_RECORDING_STORAGE_ENCRYPTION_ATTESTED", "true",
					"ATI_RECORDING_S3_ENDPOINT", "s3.example.com", "ATI_RECORDING_S3_BUCKET", "recordings")
			}
			_, err := Load(getter(with(env, kv...)))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
			if lines := strings.Count(err.Error(), "\n") + 1; lines != 1 {
				t.Fatalf("exactly one gate was broken, but %d reasons were reported:\n%v", lines, err)
			}
		})
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
