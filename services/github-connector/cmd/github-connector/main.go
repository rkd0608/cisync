// Command github-connector is Sauron's idle-until-fed check writer: it
// renders one "Agent Verification Gate" GitHub Check per decision.rendered
// pushed by control-plane (internal-protocols.md §4). Without GitHub App
// credentials it runs in dry-run mode, logging the would-be check payloads.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"sauron.dev/sauron/github-connector/internal/config"
	"sauron.dev/sauron/github-connector/internal/redact"
	"sauron.dev/sauron/github-connector/internal/server"
	pgstore "sauron.dev/sauron/github-connector/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration invalid", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// B3: every log line passes the fail-closed secret scrubber before it
	// reaches the stdout sink.
	logger := slog.New(slog.NewJSONHandler(&redact.Writer{Next: os.Stdout}, nil))

	st, err := pgstore.NewPGStore(context.Background(), cfg.PGDSN)
	if err != nil {
		logger.Error("store unavailable", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Migrate(context.Background(), migrationsDir()); err != nil {
		logger.Error("migrate failed", slog.String("err", err.Error()))
		os.Exit(1)
	}

	srv, err := server.New(cfg, st, logger)
	if err != nil {
		logger.Error("server wiring failed", slog.String("err", err.Error()))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		logger.Error("http server failed", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

// migrationsDir resolves the migration folder relative to the service root.
func migrationsDir() string {
	for _, dir := range []string{"migrations", "services/github-connector/migrations"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "migrations"
}
