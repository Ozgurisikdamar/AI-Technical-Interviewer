// Package config loads and validates control-plane configuration from the environment.
// It implements the refusal rules of ADR-020: unknown environments and unsafe production
// configurations stop the process before it serves anything.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
)

// Environment names (ADR-020).
const (
	EnvDev        = "dev"
	EnvTest       = "test"
	EnvStaging    = "staging"
	EnvProduction = "production"
)

// DevSecretPrefix marks development-only placeholder secrets (ADR-019); production refuses them.
const DevSecretPrefix = "ati-dev-"

// Retention holds explicit retention periods in days; zero means "not configured".
type Retention struct {
	TranscriptDays       int
	SessionEventsDays    int
	AssessmentOutputDays int
	AudioRecordingDays   int
}

// Recording configures the optional, fail-closed audio recording (ADR-017).
type Recording struct {
	Enabled            bool
	EncryptionAttested bool
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3UseSSL           bool
	EgressFilepathRoot string
}

// Config is the validated control-plane configuration.
type Config struct {
	Env                 string
	PublicAddr          string
	InternalAddr        string
	PublicOrigin        *url.URL
	DatabaseURL         string
	Tier1CtxDatabaseURL string

	LiveKitURL              string // URL handed to clients (ws/wss)
	LiveKitAPIURL           string // server API URL (http/https)
	LiveKitAPIKey           string
	LiveKitAPISecret        string
	LiveKitWebhookKey       string
	LiveKitWebhookSecret    string
	AgentName               string
	AgentServiceTokenSHA256 string

	Retention Retention
	Recording Recording

	PauseTimeout                 time.Duration
	CompletionDeadline           time.Duration
	LeaseTTL                     time.Duration
	LeaseRenewInterval           time.Duration
	CandidateTokenTTL            time.Duration
	InvitationTTL                time.Duration
	CandidateSessionTTL          time.Duration
	RoomDepartureTimeout         time.Duration
	AgentDispatchTimeout         time.Duration
	InvitationExchangesPerMinute int
	TrustedProxies               []netip.Prefix

	NoticeID            string
	NoticeVersion       string
	ProductionGatesFile string
	LogLevel            string
}

// IsDevLike reports whether the environment is dev or test.
func (c *Config) IsDevLike() bool { return c.Env == EnvDev || c.Env == EnvTest }

// SecureCookies reports whether cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool { return !c.IsDevLike() }

// RetentionConfigured reports whether every category used by every session has an explicit period.
func (c *Config) RetentionConfigured() bool {
	r := c.Retention
	return r.TranscriptDays > 0 && r.SessionEventsDays > 0 && r.AssessmentOutputDays > 0
}

// Getenv abstracts os.Getenv for tests.
type Getenv func(string) string

// Load reads and validates configuration. All problems are reported together.
func Load(get Getenv) (*Config, error) {
	var errs []error
	req := func(key string) string {
		v := strings.TrimSpace(get(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
		return v
	}
	opt := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	dur := func(key, def string) time.Duration {
		raw := opt(key, def)
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive duration, got %q", key, raw))
		}
		return d
	}
	days := func(key string) int {
		raw := strings.TrimSpace(get(key))
		if raw == "" {
			return 0
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > 3650 {
			errs = append(errs, fmt.Errorf("%s must be an integer between 1 and 3650", key))
			return 0
		}
		return n
	}
	boolean := func(key string) bool {
		raw := strings.ToLower(strings.TrimSpace(get(key)))
		switch raw {
		case "", "false", "0", "no":
			return false
		case "true", "1", "yes":
			return true
		}
		errs = append(errs, fmt.Errorf("%s must be true or false", key))
		return false
	}

	env := strings.TrimSpace(get("APP_ENV"))
	switch env {
	case EnvDev, EnvTest, EnvStaging, EnvProduction:
	default:
		return nil, fmt.Errorf("APP_ENV must be one of dev, test, staging, production (got %q); refusing to start", env)
	}

	c := &Config{
		Env:                     env,
		PublicAddr:              opt("ATI_PUBLIC_ADDR", ":8080"),
		InternalAddr:            opt("ATI_INTERNAL_ADDR", ":8081"),
		DatabaseURL:             req("ATI_DATABASE_URL"),
		Tier1CtxDatabaseURL:     req("ATI_TIER1_CTX_DATABASE_URL"),
		LiveKitURL:              req("ATI_LIVEKIT_URL"),
		LiveKitAPIKey:           req("ATI_LIVEKIT_API_KEY"),
		LiveKitAPISecret:        req("ATI_LIVEKIT_API_SECRET"),
		LiveKitWebhookKey:       req("ATI_LIVEKIT_WEBHOOK_API_KEY"),
		LiveKitWebhookSecret:    req("ATI_LIVEKIT_WEBHOOK_API_SECRET"),
		AgentName:               opt("ATI_AGENT_NAME", "ati-tier1-interviewer"),
		AgentServiceTokenSHA256: strings.ToLower(req("ATI_AGENT_SERVICE_TOKEN_SHA256")),
		Retention: Retention{
			TranscriptDays:       days("ATI_RETENTION_TRANSCRIPT_DAYS"),
			SessionEventsDays:    days("ATI_RETENTION_SESSION_EVENTS_DAYS"),
			AssessmentOutputDays: days("ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS"),
			AudioRecordingDays:   days("ATI_RETENTION_AUDIO_RECORDING_DAYS"),
		},
		Recording: Recording{
			Enabled:            boolean("ATI_RECORDING_ENABLED"),
			EncryptionAttested: boolean("ATI_RECORDING_STORAGE_ENCRYPTION_ATTESTED"),
			S3Endpoint:         opt("ATI_RECORDING_S3_ENDPOINT", ""),
			S3Bucket:           opt("ATI_RECORDING_S3_BUCKET", ""),
			S3AccessKey:        opt("ATI_RECORDING_S3_ACCESS_KEY", ""),
			S3SecretKey:        opt("ATI_RECORDING_S3_SECRET_KEY", ""),
			S3UseSSL:           boolean("ATI_RECORDING_S3_USE_SSL"),
			EgressFilepathRoot: opt("ATI_RECORDING_FILEPATH_ROOT", "recordings"),
		},
		// Pause timeout is product policy; 30m is a documented placeholder, not a calibrated value.
		PauseTimeout:         dur("ATI_PAUSE_TIMEOUT", "30m"),
		CompletionDeadline:   dur("ATI_COMPLETION_DEADLINE", "8s"),
		LeaseTTL:             dur("ATI_LEASE_TTL", "15s"),
		LeaseRenewInterval:   dur("ATI_LEASE_RENEW_INTERVAL", "5s"),
		CandidateTokenTTL:    dur("ATI_CANDIDATE_TOKEN_TTL", "10m"),
		InvitationTTL:        dur("ATI_INVITATION_TTL", "72h"),
		CandidateSessionTTL:  dur("ATI_CANDIDATE_SESSION_TTL", "4h"),
		RoomDepartureTimeout: dur("ATI_ROOM_DEPARTURE_TIMEOUT", "20s"),
		AgentDispatchTimeout: dur("ATI_AGENT_DISPATCH_TIMEOUT", "30s"),
		NoticeID:             opt("ATI_NOTICE_ID", "notice_default"),
		NoticeVersion:        opt("ATI_NOTICE_VERSION", "2026-11-draft.1"),
		ProductionGatesFile:  opt("ATI_PRODUCTION_GATES_FILE", ""),
		LogLevel:             opt("ATI_LOG_LEVEL", "info"),
	}

	if raw := req("ATI_PUBLIC_ORIGIN"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			errs = append(errs, fmt.Errorf("ATI_PUBLIC_ORIGIN must be a scheme://host[:port] origin, got %q", raw))
		} else {
			c.PublicOrigin = u
		}
	}
	if c.LiveKitURL != "" {
		u, err := url.Parse(c.LiveKitURL)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			errs = append(errs, fmt.Errorf("ATI_LIVEKIT_URL must be ws:// or wss://, got %q", c.LiveKitURL))
		} else {
			def := "http://" + u.Host
			if u.Scheme == "wss" {
				def = "https://" + u.Host
			}
			c.LiveKitAPIURL = opt("ATI_LIVEKIT_API_URL", def)
		}
	}
	if c.LiveKitWebhookKey != "" && c.LiveKitWebhookKey == c.LiveKitAPIKey {
		errs = append(errs, errors.New("ATI_LIVEKIT_WEBHOOK_API_KEY must differ from ATI_LIVEKIT_API_KEY (dedicated webhook key, ADR-016)"))
	}
	if c.AgentServiceTokenSHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(c.AgentServiceTokenSHA256) {
		errs = append(errs, errors.New("ATI_AGENT_SERVICE_TOKEN_SHA256 must be a hex SHA-256 digest"))
	}
	if c.LeaseRenewInterval >= c.LeaseTTL {
		errs = append(errs, errors.New("ATI_LEASE_RENEW_INTERVAL must be shorter than ATI_LEASE_TTL"))
	}
	rate, err := strconv.Atoi(opt("ATI_INVITATION_EXCHANGES_PER_MINUTE", "10"))
	if err != nil || rate < 1 || rate > 1000 {
		errs = append(errs, errors.New("ATI_INVITATION_EXCHANGES_PER_MINUTE must be between 1 and 1000"))
	}
	c.InvitationExchangesPerMinute = rate
	defProxies := ""
	if c.IsDevLike() {
		defProxies = "127.0.0.1/32,::1/128"
	}
	for _, raw := range strings.Split(opt("ATI_TRUSTED_PROXY_CIDRS", defProxies), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("ATI_TRUSTED_PROXY_CIDRS: %v", err))
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, p)
	}
	if c.Recording.Enabled {
		errs = append(errs, c.recordingErrors()...)
	}
	if c.Env == EnvProduction {
		errs = append(errs, c.productionErrors()...)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration (%s): %w", c.Env, errors.Join(errs...))
	}
	return c, nil
}

// FromEnvironment loads configuration from the process environment.
func FromEnvironment() (*Config, error) { return Load(os.Getenv) }

func (c *Config) recordingErrors() []error {
	var errs []error
	if c.Retention.AudioRecordingDays <= 0 {
		errs = append(errs, errors.New("recording enabled but ATI_RETENTION_AUDIO_RECORDING_DAYS is not set (no default retention, ADR-017)"))
	}
	if !c.Recording.EncryptionAttested {
		errs = append(errs, errors.New("recording enabled but ATI_RECORDING_STORAGE_ENCRYPTION_ATTESTED is not true"))
	}
	if c.Recording.S3Bucket == "" || c.Recording.S3Endpoint == "" {
		errs = append(errs, errors.New("recording enabled but the recording S3 endpoint/bucket is not configured"))
	}
	return errs
}

// productionErrors implements ADR-020. In this repository every shipped notice is a DRAFT, so a
// production configuration is always refused until a legally approved notice exists.
func (c *Config) productionErrors() []error {
	var errs []error
	if !c.RetentionConfigured() {
		errs = append(errs, errors.New("production requires explicit retention for transcript, session events and assessment output"))
	}
	for name, secret := range map[string]string{
		"ATI_LIVEKIT_API_SECRET":         c.LiveKitAPISecret,
		"ATI_LIVEKIT_WEBHOOK_API_SECRET": c.LiveKitWebhookSecret,
		"ATI_RECORDING_S3_SECRET_KEY":    c.Recording.S3SecretKey,
	} {
		if strings.HasPrefix(secret, DevSecretPrefix) {
			errs = append(errs, fmt.Errorf("%s is a development placeholder (%s…)", name, DevSecretPrefix))
		}
	}
	if c.PublicOrigin != nil && c.PublicOrigin.Scheme != "https" {
		errs = append(errs, errors.New("production requires an https ATI_PUBLIC_ORIGIN"))
	}
	if strings.HasPrefix(c.LiveKitURL, "ws://") {
		errs = append(errs, errors.New("production requires a wss:// ATI_LIVEKIT_URL"))
	}
	if err := c.checkNoticeApproved(); err != nil {
		errs = append(errs, err)
	}
	if err := CheckProductionGates(c.ProductionGatesFile); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (c *Config) checkNoticeApproved() error {
	files, err := contracts.ListDir(contracts.NoticesDir)
	if err != nil {
		return fmt.Errorf("notices unavailable: %w", err)
	}
	found := false
	for _, f := range files {
		var n struct {
			NoticeID string `json:"notice_id"`
			Version  string `json:"version"`
			Status   string `json:"status"`
			Purposes []struct {
				Basis string `json:"basis"`
			} `json:"purposes"`
		}
		if err := json.Unmarshal(contracts.MustRead(f), &n); err != nil {
			return err
		}
		if n.NoticeID != c.NoticeID || n.Version != c.NoticeVersion {
			continue
		}
		found = true
		if n.Status != "approved" {
			return fmt.Errorf("notice %s@%s (%s) is %s; production requires a legally approved notice", n.NoticeID, n.Version, f, n.Status)
		}
		for _, p := range n.Purposes {
			if p.Basis == "to_be_determined_by_legal" {
				return fmt.Errorf("notice %s@%s still has an undetermined lawful basis", n.NoticeID, n.Version)
			}
		}
	}
	if !found {
		return fmt.Errorf("notice %s@%s not found", c.NoticeID, c.NoticeVersion)
	}
	return nil
}

// RequiredGates are the BLUEPRINT section 20 production gates.
var RequiredGates = []string{"evaluation_validity", "security", "reliability", "privacy_legal", "human_governance"}

// CheckProductionGates verifies a human sign-off record for every production gate.
// The file format is {"gates":[{"id":"security","approved":true,"approver":"Name","approved_on":"YYYY-MM-DD"}]}.
func CheckProductionGates(path string) error {
	if path == "" {
		return errors.New("production requires ATI_PRODUCTION_GATES_FILE with human sign-off for every BLUEPRINT section 20 gate")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("production gates file: %w", err)
	}
	var f struct {
		Gates []struct {
			ID         string `json:"id"`
			Approved   bool   `json:"approved"`
			Approver   string `json:"approver"`
			ApprovedOn string `json:"approved_on"`
		} `json:"gates"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("production gates file: %w", err)
	}
	ok := map[string]bool{}
	for _, g := range f.Gates {
		if _, err := time.Parse("2006-01-02", g.ApprovedOn); g.Approved && strings.TrimSpace(g.Approver) != "" && err == nil {
			ok[g.ID] = true
		}
	}
	var missing []string
	for _, g := range RequiredGates {
		if !ok[g] {
			missing = append(missing, g)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("production gates without human sign-off: %v", missing)
	}
	return nil
}
