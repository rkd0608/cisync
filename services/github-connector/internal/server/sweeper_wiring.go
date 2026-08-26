package server

import (
	"context"
	"log/slog"

	"cisync.dev/cisync/github-connector/internal/config"
	"cisync.dev/cisync/github-connector/internal/emit"
	"cisync.dev/cisync/github-connector/internal/sweeper"
	"cisync.dev/cisync/github-connector/internal/tracking"
)

// newSweeperRunner builds the stalled-check sweeper loop closure, keeping
// server.go focused on wiring.
func newSweeperRunner(cfg *config.Config, tracker tracking.Store, router *emit.Router, logger *slog.Logger) func(context.Context) {
	sw := sweeper.New(tracker, router, cfg.DetailsURL,
		cfg.StalledCheckAge, cfg.SweepInterval, nil, logger)
	return sw.Run
}
