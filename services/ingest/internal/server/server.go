// Package server assembles the ingest HTTP mux and lifecycle components.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"cisync.dev/cisync/ingest/internal/api"
	"cisync.dev/cisync/ingest/internal/config"
	"cisync.dev/cisync/ingest/internal/forward"
	"cisync.dev/cisync/ingest/internal/obs"
	"cisync.dev/cisync/ingest/internal/retry"
	"cisync.dev/cisync/ingest/internal/seen"
	"cisync.dev/cisync/ingest/internal/store"
)

// Server bundles everything the ingest process needs at runtime.
type Server struct {
	HTTP    *http.Server
	Retry   *retry.Worker
	Metrics *obs.Metrics
	store   store.Store
	hook    *api.GitHubHookHandler
}

// Close releases handler-owned background resources (the B7 audit-marker
// queue); called by Run after HTTP serving ended.
func (s *Server) Close() {
	if s.hook != nil {
		s.hook.Close()
	}
}

// New wires the webhook edge, health/metrics endpoints, and the retry worker
// from configuration and the provided store.
func New(cfg config.Config, st store.Store, logger *slog.Logger, metrics *obs.Metrics) *Server {
	forwarder := forward.New(cfg.ControlURL, []byte(cfg.WebhookSecret))
	secretBytes := make([][]byte, len(cfg.WebhookSecrets))
	for i, s := range cfg.WebhookSecrets {
		secretBytes[i] = []byte(s)
	}
	// H2 replay seen-window: bounded LRU keyed by payload class hash.
	seenCache := seen.New(cfg.SeenMaxEntries, cfg.SeenWindowTTL, time.Now)
	hook := api.NewGitHubHookHandler(st, forwarder, metrics, logger, time.Now,
		cfg.MaxBodyBytes, cfg.TimestampSkew, secretBytes, seenCache)
	retryWorker := retry.NewWorker(st, forwarder, metrics, logger, cfg.RetryInterval, cfg.RetryBase, cfg.MaxAttempts, time.Now)

	mux := http.NewServeMux()
	// The bare path is the GitHub-facing webhook route; /v1/hooks/github
	// remains the versioned alias so existing producers keep working.
	mux.Handle("POST /hooks/github", hook)
	mux.Handle("POST /v1/hooks/github", hook)
	mux.Handle("GET /healthz", api.NewHealthzHandler())
	mux.Handle("GET /metrics", api.NewMetricsHandler(metrics))

	return &Server{
		HTTP: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
		},
		Retry:   retryWorker,
		Metrics: metrics,
		store:   st,
		hook:    hook,
	}
}

// Run blocks serving HTTP until ctx is cancelled, then shuts down gracefully.
// The caller owns the retry worker lifecycle (Run it on its own goroutine).
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.HTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	var runErr error
	select {
	case runErr = <-errCh:
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.HTTP.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown failed", slog.String("err", err.Error()))
		}
	}
	// Drain handler-owned background work only after the listener stopped
	// so in-flight requests could still enqueue markers.
	s.Close()
	return runErr
}
