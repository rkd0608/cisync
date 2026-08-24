// Command runner-fleet is the execution plane: it serves the fenced worker
// protocol and runs jobs via the sim (default) or docker provider.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/config"
	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/joblease"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	"sauron.dev/sauron/runner-fleet/internal/providers"
	"sauron.dev/sauron/runner-fleet/internal/redact"
	"sauron.dev/sauron/runner-fleet/internal/server"
	pgstore "sauron.dev/sauron/runner-fleet/internal/store"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("configuration invalid", slog.String("err", err.Error()))
		os.Exit(1)
	}

	logOut := &redact.Writer{Next: os.Stdout}
	logger := slog.New(slog.NewJSONHandler(logOut, nil))
	metrics := obs.New()

	var provider domain.Provider
	switch cfg.Provider {
	case "docker":
		logger.Warn("docker provider is NOT-FOR-PRODUCTION (THREAT_MODEL B5); dev/demo only")
		provider = providers.NewDocker(cfg.DockerBin, cfg.DockerImage)
	default:
		provider = providers.NewSim()
	}

	st, err := pgstore.NewPGStore(context.Background(), cfg.PGDSN)
	if err != nil {
		logger.Error("store unavailable", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Migrate(context.Background(), pgstore.MigrationsDir()); err != nil {
		logger.Error("migrate failed", slog.String("err", err.Error()))
		os.Exit(1)
	}

	// Job-lease verification is REQUIRED at boot (THREAT_MODEL B2/I-04):
	// without the control-plane public key every mutation would 401 anyway,
	// so refusing to start surfaces the misconfiguration immediately.
	if len(cfg.JobLeasePubKey) == 0 {
		logger.Error("job-lease public key not configured (SAURON_FLEET_JOBLEASYPUB_KEY_FILE or SAURON_FLEET_JOBLEASE_PUB_B64)")
		os.Exit(1)
	}
	leaseVerifier, err := joblease.NewVerifierFromPublicPEM(cfg.JobLeasePubKey)
	if err != nil {
		logger.Error("job-lease public key invalid", slog.String("err", err.Error()))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, st, provider, logger, metrics, time.Now, leaseVerifier)

	go srv.Executor.Run(ctx)
	go srv.Sweeper(ctx, cfg.WorkerStaleAfter, cfg.PollInterval)
	go srv.GaugeQueueDepth(ctx)

	if err := srv.Run(ctx); err != nil {
		logger.Error("http server failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
}
