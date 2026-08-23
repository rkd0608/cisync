// Command ingest terminates GitHub webhooks: signature verification, raw
// delivery persistence, and async forwarding to control-plane.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"sauron.dev/sauron/ingest/internal/config"
	"sauron.dev/sauron/ingest/internal/obs"
	"sauron.dev/sauron/ingest/internal/redact"
	"sauron.dev/sauron/ingest/internal/server"
	pgstore "sauron.dev/sauron/ingest/internal/store"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, st, logger, metrics)

	go srv.Retry.Run(ctx)
	go srv.GaugeDeliveries(ctx, cfg.RetryInterval)

	if err := srv.Run(ctx); err != nil {
		logger.Error("http server failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
}
