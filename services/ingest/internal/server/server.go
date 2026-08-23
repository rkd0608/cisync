// Package server assembles the ingest HTTP mux and lifecycle components.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/ingest/internal/api"
	"sauron.dev/sauron/ingest/internal/config"
	"sauron.dev/sauron/ingest/internal/forward"
	"sauron.dev/sauron/ingest/internal/obs"
	"sauron.dev/sauron/ingest/internal/retry"
	"sauron.dev/sauron/ingest/internal/store"
)

// Server bundles everything the ingest process needs at runtime.
type Server struct {
	HTTP    *http.Server
	Retry   *retry.Worker
	Metrics *obs.Metrics
	store   store.Store
}

// New wires the webhook edge, health/metrics endpoints, and the retry worker
// from configuration and the provided store.
func New(cfg config.Config, st store.Store, logger *slog.Logger, metrics *obs.Metrics) *Server {
	forwarder := forward.New(cfg.ControlURL, []byte(cfg.WebhookSecret))
	hook := api.NewGitHubHookHandler(st, forwarder, metrics, logger, time.Now, cfg.MaxBodyBytes, cfg.TimestampSkew)
	retryWorker := retry.NewWorker(st, forwarder, metrics, logger, cfg.RetryInterval, cfg.RetryBase, cfg.MaxAttempts, time.Now)

	mux := http.NewServeMux()
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
	return runErr
}
