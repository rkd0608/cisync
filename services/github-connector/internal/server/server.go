// Package server assembles the github-connector HTTP mux.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/github-connector/internal/api"
	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/config"
	"sauron.dev/sauron/github-connector/internal/ghauth"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/store"

	"github.com/google/go-github/v66/github"
)

// Server bundles everything the connector process needs at runtime. The
// service is idle-until-fed: it only reacts to decision pushes.
type Server struct {
	HTTP    *http.Server
	Metrics *obs.Metrics
}

// New wires the decisions endpoint, health/metrics, and the publisher for the
// configured mode (dry-run without GitHub App credentials).
func New(cfg *config.Config, st store.Store, logger *slog.Logger) (*Server, error) {
	metrics := obs.New()
	logger = logger.With(slog.Bool("dry_run", cfg.DryRun))

	var publisher checks.Publisher
	if cfg.DryRun {
		publisher = checks.NewDryRunPublisher(logger)
	} else {
		tokenSource, err := ghauth.NewInstallationTokenSource(
			cfg.GitHubAppID, cfg.GitHubAppPrivateKeyFile, cfg.GitHubInstallationID)
		if err != nil {
			return nil, err
		}
		client := github.NewClient(&http.Client{
			Timeout:   15 * time.Second,
			Transport: &installationTokenTransport{source: tokenSource},
		})
		publisher = checks.NewLivePublisher(client, logger)
	}

	decisions := api.NewDecisionsHandler(st, publisher, metrics, logger,
		cfg.WebhookSecret, cfg.DetailsURL, cfg.DryRun)

	mux := http.NewServeMux()
	mux.Handle("POST /internal/connector/decisions", decisions)
	mux.Handle("GET /healthz", api.NewHealthzHandler())
	mux.Handle("GET /metrics", api.NewMetricsHandler(metrics))

	return &Server{
		HTTP: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
		},
		Metrics: metrics,
	}, nil
}

// Run blocks serving HTTP until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
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
		return runErr
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.HTTP.Shutdown(shutdownCtx)
	}
}

// installationTokenTransport injects a short-lived installation token on
// every GitHub API call.
type installationTokenTransport struct {
	source *ghauth.InstallationTokenSource
}

func (t *installationTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultTransport.RoundTrip(req)
}
