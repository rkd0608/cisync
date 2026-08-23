package api

import (
	"net/http"
	"time"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
	plannerengine "sauron.dev/sauron/control-plane/internal/planner"
	"sauron.dev/sauron/control-plane/internal/store"
)

// Server wires HTTP handlers to the store and domain ports.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	planner domain.Planner
	metrics *Metrics
	started time.Time
}

// NewServer constructs the server; planner may be nil to use the real
// selection engine backed by the compiled-in default policy registry.
func NewServer(cfg *config.Config, st *store.Store, planner domain.Planner) *Server {
	if planner == nil {
		planner = plannerengine.NewEnginePlanner(nil)
	}
	return &Server{
		cfg:     cfg,
		store:   st,
		planner: planner,
		metrics: NewMetrics(),
		started: time.Now(),
	}
}

// Handler builds the full route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	mux.HandleFunc("POST /v1/change-intents", s.requireAuth(s.handleCreateIntent))
	mux.HandleFunc("GET /v1/change-intents/{intentId}", s.requireAuth(s.handleGetIntent))
	mux.HandleFunc("GET /v1/change-intents/{intentId}/candidates", s.requireAuth(s.handleListCandidates))
	mux.HandleFunc("POST /v1/change-intents/{intentId}/candidates", s.requireAuth(s.handleSubmitCandidate))
	mux.HandleFunc("GET /v1/candidates/{candidateId}", s.requireAuth(s.handleGetCandidate))
	mux.HandleFunc("GET /v1/candidates/{candidateId}/dossier", s.requireAuth(s.handleGetDossier))
	mux.HandleFunc("GET /v1/clusters/{clusterId}", s.requireAuth(s.handleGetCluster))
	mux.HandleFunc("POST /v1/leases/{leaseId}/renew", s.requireAuth(s.handleRenewLease))
	mux.HandleFunc("DELETE /v1/leases/{leaseId}", s.requireAuth(s.handleReleaseLease))
	mux.HandleFunc("GET /v1/events", s.requireAuth(s.handleTailEvents))

	mux.HandleFunc("POST /v1/hooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("POST /internal/ctrl/deliveries", s.handleDelivery)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "resource not found", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "404")
	})
	return mux
}
