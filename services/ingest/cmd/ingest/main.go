// Command ingest terminates GitHub webhooks: signature verification, raw
// delivery persistence, and async forwarding to control-plane.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"cisync.dev/cisync/ingest/internal/config"
	"cisync.dev/cisync/ingest/internal/obs"
	"cisync.dev/cisync/ingest/internal/redact"
	"cisync.dev/cisync/ingest/internal/server"
	pgstore "cisync.dev/cisync/ingest/internal/store"
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

	// H4: track background workers so the PG pool closes only after they
	// stopped issuing queries (SIGTERM ⇒ drain HTTP → wait workers → close).
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		srv.Retry.Run(ctx)
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		srv.GaugeDeliveries(ctx, cfg.RetryInterval)
	}()

	if err := srv.Run(ctx); err != nil {
		logger.Error("http server failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
	stop() // release signal handling before waiting on workers
	workers.Wait()
}
