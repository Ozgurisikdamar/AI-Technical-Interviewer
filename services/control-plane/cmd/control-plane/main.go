// Command control-plane serves the public API and the internal Tier-1 API (ADR-016).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/config"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/flow"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/interview"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/logging"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/outbox"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/realtime"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/recording"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/tier1ctx"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("control plane stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

// enforceLeastPrivilege makes a failed database least-privilege check fatal in production and a
// warning elsewhere (ADR-020): dev and test clusters often connect as a superuser.
func enforceLeastPrivilege(env string, checkErr error, log *slog.Logger) error {
	if checkErr == nil {
		return nil
	}
	if env == config.EnvProduction {
		return checkErr
	}
	log.Warn("least-privilege check failed (enforced in production)", slog.Any("error", checkErr))
	return nil
}

func run() error {
	cfg, err := config.FromEnvironment() // refuses unknown environments and unsafe production setups
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "control-plane")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := store.Open(ctx, cfg.DatabaseURL, 30)
	if err != nil {
		return err
	}
	defer pool.Close()
	tier1Pool, err := store.Open(ctx, cfg.Tier1CtxDatabaseURL, 10)
	if err != nil {
		return err
	}
	defer tier1Pool.Close()
	if err := enforceLeastPrivilege(cfg.Env, store.VerifyLeastPrivilege(ctx, pool), log); err != nil {
		return err
	}

	machine, err := flow.Default()
	if err != nil {
		return err
	}
	media := realtime.NewLiveKit(realtime.Options{
		PublicURL: cfg.LiveKitURL, APIURL: cfg.LiveKitAPIURL, APIKey: cfg.LiveKitAPIKey, APISecret: cfg.LiveKitAPISecret,
		WebhookKey: cfg.LiveKitWebhookKey, WebhookSecret: cfg.LiveKitWebhookSecret, AgentName: cfg.AgentName,
		CandidateTTL: cfg.CandidateTokenTTL, DepartureTimeout: cfg.RoomDepartureTimeout,
	})
	background := &api.Background{} // asynchronous effects, drained on shutdown
	// Recording is off unless enabled and every deployment precondition was validated by config (ADR-017).
	var rec *recording.Recorder
	var recorder api.Recorder
	if cfg.Recording.Enabled {
		rec = recording.New(pool, media, realtime.NewLiveKitEgress(cfg.LiveKitAPIURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret), recording.Settings{
			Enabled: true, EncryptionAttested: cfg.Recording.EncryptionAttested, RetentionDays: cfg.Retention.AudioRecordingDays,
		}, log, nil)
		recorder = rec
	}
	svc, err := interview.New(pool, machine, interview.Settings{
		CompletionDeadline: cfg.CompletionDeadline, PauseTimeout: cfg.PauseTimeout, InvitationTTL: cfg.InvitationTTL,
		CandidateSessionTTL: cfg.CandidateSessionTTL, LeaseTTL: cfg.LeaseTTL, AgentDispatchTimeout: cfg.AgentDispatchTimeout, Retention: cfg.Retention,
		RecordingEnabled: cfg.Recording.Enabled, RecordingAttested: cfg.Recording.EncryptionAttested,
		NoticeID: cfg.NoticeID, NoticeVersion: cfg.NoticeVersion,
	}, api.Effects{Media: media, Log: log, Rec: recorder, Background: background}, log, nil)
	if err != nil {
		return err
	}
	metrics := api.NewMetrics()
	svc.SetObserver(metrics.Transition)
	// The outbox reaches Tier-2 only when Redis is configured (ADR-022); otherwise it stays pending.
	var relay api.Relay
	if cfg.RedisURL != "" {
		pub, err := outbox.NewRedisPublisher(cfg.RedisURL)
		if err != nil {
			return err
		}
		defer pub.Close()
		relay = outbox.NewRelay(pool, pub, svc, log, nil)
	}
	srv := api.New(api.Deps{Config: cfg, Service: svc, Tier1: tier1ctx.New(tier1Pool, nil), Media: media, Pool: pool, Tier1Pool: tier1Pool, Log: log, Metrics: metrics,
		Recorder: recorder, Relay: relay})

	public := &http.Server{Addr: cfg.PublicAddr, Handler: srv.PublicHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second}
	internal := &http.Server{Addr: cfg.InternalAddr, Handler: srv.InternalHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	for _, s := range []*http.Server{public, internal} {
		go func(s *http.Server) {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}(s)
	}
	sweepers := make(chan struct{})
	go func() {
		defer close(sweepers)
		srv.RunSweepers(ctx)
	}()
	log.Info("control plane started", slog.String("env", cfg.Env), slog.String("public", cfg.PublicAddr), slog.String("internal", cfg.InternalAddr))
	if cfg.Env != config.EnvProduction {
		log.Warn("pre-production build: not for use with real candidates (ADR-020)")
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		return err
	}
	drain(log, shutdownGrace, sweepers, background, public, internal)
	return nil
}

// writeTimeout exceeds the slowest bounded handler, so a slow media server yields an error response
// rather than a dropped connection: an agent dispatch makes up to six media calls of at most
// realtime.DefaultCallTimeout (5 s) each; a microphone webhook may wait for an egress start (10 s).
const writeTimeout = 60 * time.Second

// shutdownGrace bounds a graceful stop: in-flight requests (and the effects they run after
// commit), the sweepers' current pass and asynchronous effects such as a recording start, whose
// egress call is bounded by its own timeout (10 s by default).
const shutdownGrace = 20 * time.Second

// drain stops accepting requests, then waits for in-flight requests, the sweepers and tracked
// background work. Whatever is still running when the grace ends is cut off; recordings left
// starting or stopping are reconciled at the next start.
func drain(log *slog.Logger, grace time.Duration, sweepers <-chan struct{}, background *api.Background, servers ...*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	errs := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *http.Server) { errs <- s.Shutdown(ctx) }(s)
	}
	var failed []error
	for range servers {
		if err := <-errs; err != nil {
			failed = append(failed, err)
		}
	}
	select {
	case <-sweepers:
	case <-ctx.Done():
		failed = append(failed, errors.New("sweepers still running"))
	}
	if err := background.Wait(ctx); err != nil {
		failed = append(failed, errors.New("background work still running"))
	}
	if len(failed) > 0 {
		log.Warn("shutdown grace ended before all work finished", slog.Any("error", errors.Join(failed...)))
		return
	}
	log.Info("control plane stopped")
}
