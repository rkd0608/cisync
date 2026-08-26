// Package server assembles the github-connector HTTP mux plus its
// background loops (pending-write drainer, stalled-check sweeper).
package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"sauron.dev/sauron/github-connector/internal/api"
	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/config"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/ghauth"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/queue"
	"sauron.dev/sauron/github-connector/internal/ratelimit"
	"sauron.dev/sauron/github-connector/internal/redact"
	"sauron.dev/sauron/github-connector/internal/rerun"
	"sauron.dev/sauron/github-connector/internal/statusapi"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// Deps carries the state stores owned by the caller so tests can inject
// fakes; production wires the PG-backed adapters from internal/store
// (NewTracker / NewPendingQueue / PGStore directly for Resolve+Status).
type Deps struct {
	Tracker tracking.Store            // required
	Pending queue.Store               // optional; nil ⇒ budget exhaustion 503s instead of queuing
	Resolve emit.InstallationResolver // optional; nil ⇒ fail-closed dry-run everywhere
	// Status optionally backs GET /v1/installations/status (W5-A); nil ⇒ the
	// route is not registered rather than serving an empty projection.
	Status statusapi.StatusSource
	Stdout io.Writer // optional sink override for tests
}

// Server bundles everything the connector process needs at runtime.
type Server struct {
	HTTP    *http.Server
	Metrics *obs.Metrics

	drainer *queue.Drainer
	sweeper *sweeperLoop
}

type sweeperLoop struct{ run func(context.Context) }

// New wires the §4 endpoint, health/metrics, publication paths, and the
// background loops per configured mode.
func New(cfg *config.Config, deps Deps, logger *slog.Logger) (*Server, error) {
	metrics := obs.New()
	logger = logger.With(slog.Bool("dry_run", cfg.DryRun))

	stdout := deps.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	dry := checks.NewDryRunPublisher(&redact.Writer{Next: stdout})

	var registry *ghauth.Registry
	if !cfg.DryRun {
		registry = ghauth.NewRegistry(cfg.GitHubAppID, cfg.GitHubAppPrivateKeyFile,
			ghauth.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
		if err := registry.Seed(cfg.GitHubInstallationID); err != nil {
			return nil, err
		}
	}

	budget := ratelimit.NewBudget(cfg.WriteBudgetPerHour, time.Now)
	gate := ratelimit.NewGate(budget, logger)
	router := emit.NewRouter(dry, deps.Resolve, registry, gate, budget, deps.Pending, metrics, logger)

	rerunBudget := rerun.NewBudget(cfg.RerunMaxPerCandidate, cfg.RerunRatePerHour, time.Now)
	rerunSeen := rerun.NewDedupe(24*time.Hour, time.Now)
	rerunControl := rerun.NewControl(cfg.CtrlBaseURL, cfg.CtrlToken,
		&http.Client{Timeout: 10 * time.Second}, time.Now)

	handler := api.NewDecisionsHandler(cfg.WebhookSecret, api.HandlerDeps{
		Tracker:      deps.Tracker,
		Router:       router,
		Metrics:      metrics,
		DetailsURL:   cfg.DetailsURL,
		RerunPolicy:  cfg.RerunPolicy,
		RerunBudget:  rerunBudget,
		RerunControl: rerunControl,
		RerunSeen:    rerunSeen,
	}, logger)

	mux := http.NewServeMux()
	mux.Handle("POST /internal/connector/decisions", handler)
	// W5-A: installation status served from ghconn tables; fails closed via
	// statusapi when no admin token is configured.
	if deps.Status != nil {
		mux.Handle("GET /v1/installations/status", statusapi.NewHandler(deps.Status, cfg.AdminToken))
	}
	mux.Handle("GET /healthz", api.NewHealthzHandler())
	mux.Handle("GET /metrics", api.NewMetricsHandler(metrics))

	srv := &Server{
		HTTP: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
		},
		Metrics: metrics,
	}
	if deps.Pending != nil {
		srv.drainer = queue.NewDrainer(deps.Pending, gate, budget, deliverFunc(router),
			cfg.PendingDrainInterval, time.Now, logger, nil)
	}
	srv.sweeper = &sweeperLoop{run: newSweeperRunner(cfg, deps.Tracker, router, logger)}
	return srv, nil
}

func deliverFunc(router *emit.Router) queue.Deliver {
	return func(ctx context.Context, w queue.PendingWrite) error {
		_, err := router.PublishDirect(ctx, w.Repo, w.CheckRunID, w.Payload)
		return err
	}
}

// Run blocks serving HTTP until ctx is cancelled, running background loops
// alongside; graceful shutdown on ctx done.
func (s *Server) Run(ctx context.Context) error {
	loopCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-ctx.Done()
		cancel()
	}()

	// H4: the background loops write through the same PG pool main closes;
	// WAIT for them after the HTTP drain instead of abandoning goroutines.
	var loops sync.WaitGroup
	if s.drainer != nil {
		loops.Add(1)
		go func() {
			defer loops.Done()
			s.drainer.Run(loopCtx)
		}()
	}
	loops.Add(1)
	go func() {
		defer loops.Done()
		s.sweeper.run(loopCtx)
	}()

	errCh := make(chan error, 1)
	go func() {
		if err := s.HTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case runErr := <-errCh:
		cancel()
		loops.Wait()
		return runErr
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		err := s.HTTP.Shutdown(shutdownCtx)
		cancel()
		loops.Wait()
		return err
	}
}
