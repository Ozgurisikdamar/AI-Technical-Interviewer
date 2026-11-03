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
	if err := store.VerifyLeastPrivilege(ctx, pool); err != nil {
		if cfg.Env == config.EnvProduction {
			return err
		}
		log.Warn("least-privilege check failed (enforced in production)", slog.Any("error", err))
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
	}, api.Effects{Media: media, Log: log, Rec: recorder}, log, nil)
	if err != nil {
		return err
	}
	metrics := api.NewMetrics()
	svc.SetObserver(metrics.Transition)
	srv := api.New(api.Deps{Config: cfg, Service: svc, Tier1: tier1ctx.New(tier1Pool, nil), Media: media, Pool: pool, Tier1Pool: tier1Pool, Log: log, Metrics: metrics,
		Recorder: recorder})

	public := &http.Server{Addr: cfg.PublicAddr, Handler: srv.PublicHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	internal := &http.Server{Addr: cfg.InternalAddr, Handler: srv.InternalHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	for _, s := range []*http.Server{public, internal} {
		go func(s *http.Server) {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}(s)
	}
	go srv.RunSweepers(ctx)
	log.Info("control plane started", slog.String("env", cfg.Env), slog.String("public", cfg.PublicAddr), slog.String("internal", cfg.InternalAddr))
	if cfg.Env != config.EnvProduction {
		log.Warn("pre-production build: not for use with real candidates (ADR-020)")
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdown)
	_ = internal.Shutdown(shutdown)
	return nil
}
