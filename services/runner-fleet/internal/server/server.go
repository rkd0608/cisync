// Package server assembles the runner-fleet HTTP mux and execution runtime.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/api"
	"cisync.dev/cisync/runner-fleet/internal/config"
	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/execute"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/obs"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// Server bundles everything the fleet process needs at runtime. HTTP exposes
// the assembled mux for httptest reuse; Run serves it.
type Server struct {
	HTTP      *http.Server
	Mux       http.Handler
	Executor  *execute.Executor
	Registry  *execute.Registry
	Sweeper   sweeperFunc
	Metrics   *obs.Metrics
	store     store.Store
	gaugeTick time.Duration
	logger    *slog.Logger
}

type sweeperFunc func(ctx context.Context, threshold, interval time.Duration)

// New wires protocol endpoints, the embedded executor, and metrics from
// configuration. providerName is sim|docker; provider must match it. nowFn is
// injectable for deterministic tests. leaseVerifier validates job-lease
// credentials (B2/I-04); nil fails closed (every mutation 401s).
func New(cfg config.Config, st store.Store, p domain.Provider, logger *slog.Logger, metrics *obs.Metrics, nowFn func() time.Time, leaseVerifier *joblease.Verifier) *Server {
	if nowFn == nil {
		nowFn = time.Now
	}
	registry := execute.NewRegistry()
	exec := execute.New(st, p, registry, metrics, logger, cfg.Pool, cfg.Provider,
		cfg.SimWorkers, cfg.HeartbeatInterval, cfg.PollInterval, leaseVerifier)

	mux := http.NewServeMux()
	mux.Handle("POST /internal/fleet/jobs/claim", api.NewClaimHandler(st, logger, nowFn))
	mux.Handle("POST /internal/fleet/jobs", api.NewEnqueueHandler(st, logger, nowFn))
	mux.Handle("POST /internal/fleet/jobs/{run_id}/heartbeat", api.NewHeartbeatHandler(st, metrics, nowFn, leaseVerifier))
	mux.Handle("POST /internal/fleet/jobs/{run_id}/complete", api.NewCompleteHandler(st, metrics, logger, nowFn, leaseVerifier))
	mux.Handle("POST /internal/fleet/jobs/{run_id}/cancel", api.NewCancelHandler(st, registry, p, metrics, logger, nowFn, leaseVerifier))
	mux.Handle("GET /internal/fleet/jobs/completed", api.NewCompletedHandler(st, logger))
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
		Mux:       mux,
		Executor:  exec,
		Registry:  registry,
		Sweeper:   exec.SweepStale,
		Metrics:   metrics,
		store:     st,
		gaugeTick: cfg.GaugeInterval,
		logger:    logger,
	}
}

// Run blocks serving HTTP until ctx is cancelled, then shuts down gracefully.
// The executor, sweeper, and gauges run on their own goroutines via Start.
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

// GaugeQueueDepth refreshes fleet_queue_depth{pool,tier} until ctx ends.
func (s *Server) GaugeQueueDepth(ctx context.Context) {
	ticker := time.NewTicker(s.gaugeTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gaugeOnce()
		}
	}
}

func (s *Server) gaugeOnce() {
	depth, err := s.store.QueueDepth(context.Background())
	if err != nil {
		return
	}
	for key, n := range depth {
		var pool string
		var tier string
		for i := 0; i < len(key); i++ {
			if key[i] == '/' {
				pool = key[:i]
				tier = key[i+1:]
				break
			}
		}
		s.Metrics.GaugeSet("fleet_queue_depth", "Queued jobs per pool and tier", float64(n), "pool", pool, "tier", tier)
	}
}
