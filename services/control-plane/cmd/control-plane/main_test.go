package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain lets a test re-execute this binary as the real server (ATI_SMOKE_RUN_MAIN=1), so
// startup refusals are checked on the process exit code and output, not on a helper function.
func TestMain(m *testing.M) {
	if os.Getenv("ATI_SMOKE_RUN_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func runServer(t *testing.T, env ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append([]string{"ATI_SMOKE_RUN_MAIN=1", "PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the server did not stop: it must refuse this configuration\n%s", out)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

const sentinel = "SENTINEL7f3a"

func baseEnv(appEnv string) []string {
	return []string{
		"APP_ENV=" + appEnv,
		"ATI_PUBLIC_ORIGIN=https://interview.example.com",
		"ATI_DATABASE_URL=postgres://ati_control_plane:pw-" + sentinel + "@127.0.0.1:1/ati?sslmode=disable&connect_timeout=2",
		"ATI_TIER1_CTX_DATABASE_URL=postgres://ati_tier1_ctx:pw-" + sentinel + "@127.0.0.1:1/ati?sslmode=disable&connect_timeout=2",
		"ATI_LIVEKIT_URL=wss://media.example.com",
		"ATI_LIVEKIT_API_KEY=key",
		"ATI_LIVEKIT_API_SECRET=ati-dev-api-" + sentinel,
		"ATI_LIVEKIT_WEBHOOK_API_KEY=webhook-key",
		"ATI_LIVEKIT_WEBHOOK_API_SECRET=ati-dev-webhook-" + sentinel,
		"ATI_AGENT_SERVICE_TOKEN_SHA256=" + strings.Repeat("ab", 32),
		"ATI_RETENTION_TRANSCRIPT_DAYS=30", "ATI_RETENTION_SESSION_EVENTS_DAYS=30", "ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS=30",
	}
}

func TestStartupRefusesWithoutAnEnvironment(t *testing.T) {
	code, out := runServer(t, baseEnv("")[1:]...)
	if code == 0 || !strings.Contains(out, "refusing to start") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestStartupRefusesAnUnsafeProductionConfigurationWithoutLeakingSecrets(t *testing.T) {
	code, out := runServer(t, baseEnv("production")...)
	if code == 0 {
		t.Fatalf("production started with draft notices and development secrets:\n%s", out)
	}
	for _, want := range []string{"development placeholder", "legally approved notice", "human sign-off"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should name %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("a secret value reached the output:\n%s", out)
	}
}

func TestStartupFailsClosedWhenTheDatabaseIsUnreachable(t *testing.T) {
	code, out := runServer(t, baseEnv("staging")...)
	if code == 0 || !strings.Contains(out, "control plane stopped") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("the database password reached the output:\n%s", out)
	}
}

func TestLeastPrivilegeIsEnforcedOnlyInProduction(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	failed := errors.New("database role is a superuser")
	if err := enforceLeastPrivilege("production", failed, log); !errors.Is(err, failed) {
		t.Fatalf("production must refuse: %v", err)
	}
	for _, env := range []string{"dev", "test", "staging"} {
		logs.Reset()
		if err := enforceLeastPrivilege(env, failed, log); err != nil || !strings.Contains(logs.String(), "enforced in production") {
			t.Fatalf("%s: err=%v logs=%s", env, err, logs.String())
		}
	}
	if err := enforceLeastPrivilege("production", nil, log); err != nil {
		t.Fatal(err)
	}
}
