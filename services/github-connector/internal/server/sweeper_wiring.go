package server

import (
	"context"
	"log/slog"

	"sauron.dev/sauron/github-connector/internal/config"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/sweeper"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// newSweeperRunner builds the stalled-check sweeper loop closure, keeping
// server.go focused on wiring.
func newSweeperRunner(cfg *config.Config, tracker tracking.Store, router *emit.Router, logger *slog.Logger) func(context.Context) {
	sw := sweeper.New(tracker, router, cfg.DetailsURL,
		cfg.StalledCheckAge, cfg.SweepInterval, nil, logger)
	return sw.Run
}
