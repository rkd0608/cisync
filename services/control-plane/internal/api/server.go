package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/control-plane/internal/audit"
	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
	plannerengine "sauron.dev/sauron/control-plane/internal/planner"
	"sauron.dev/sauron/control-plane/internal/store"
)

// auditStopTimeout bounds the graceful drain of the security-audit stream at
// shutdown: long enough to flush a full buffer against local PG, short
// enough to keep SIGTERM turnaround well inside the orchestrator budget.
const auditStopTimeout = 3 * time.Second

// Server wires HTTP handlers to the store and domain ports.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	planner domain.Planner
	metrics *Metrics
	audit   *audit.Stream
	started time.Time
}

// NewServer constructs the server; planner may be nil to use the real
// selection engine backed by the compiled-in default policy registry.
// The server owns the dedicated security-audit stream (THREAT_MODEL B7):
// fire-and-forget emissions persist via the store sink with drop-oldest
// shedding under flood.
func NewServer(cfg *config.Config, st *store.Store, planner domain.Planner) *Server {
	if planner == nil {
		planner = plannerengine.NewEnginePlanner(nil)
	}
	s := &Server{
		cfg:     cfg,
		store:   st,
		planner: planner,
		metrics: NewMetrics(),
		started: time.Now(),
	}
	s.audit = audit.NewStream(audit.DefaultCapacity,
		func(ctx context.Context, ev audit.Event) error {
			if s.store == nil {
				// Store-less constructions are test-only; nothing to
				// persist, so the event counts as dropped.
				return errAuditNoStore
			}
			return s.store.InsertSecurityAudit(ctx, ev)
		},
		audit.Hooks{
			OnEmitted: func(kind audit.Kind) {
				s.metrics.Add("sauron_security_audit_total", 1, "kind", string(kind))
			},
			OnDropped: func(kind audit.Kind) {
				s.metrics.Add("sauron_security_audit_dropped_total", 1, "kind", string(kind))
			},
		})
	return s
}

var errAuditNoStore = auditSinkError("no store wired")

type auditSinkError string

func (e auditSinkError) Error() string { return string(e) }

// Audit exposes the security-audit stream for lifecycle management (main
// stops it during graceful shutdown) and for H3 verify-scheduler wiring.
func (s *Server) Audit() *audit.Stream { return s.audit }

// Metrics exposes the registry so background workers outside this package
// (scheduler, verifier) can share ONE consistent exposition endpoint.
func (s *Server) Metrics() *Metrics { return s.metrics }

// UseAuditSink replaces the audit stream's persistence sink. Test seam:
// httptest emission assertions capture events without a database.
func (s *Server) UseAuditSink(sink audit.Sink) {
	s.audit.ReplaceSink(sink)
}

// StopAudit drains and stops the audit stream; called by main on shutdown
// after HTTP serving ended so in-flight requests can still emit.
func (s *Server) StopAudit() {
	if flushed := s.audit.Stop(auditStopTimeout); !flushed {
		slog.Warn("security audit stream flush timed out; buffered rows lost",
			slog.Duration("timeout", auditStopTimeout))
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
	mux.HandleFunc("POST /v1/candidates/{candidateId}/revalidate", s.requireAuth(s.handleRevalidate))
	mux.HandleFunc("GET /v1/candidates/{candidateId}/dossier", s.requireAuth(s.handleGetDossier))
	mux.HandleFunc("GET /v1/policies/active", s.requireAuth(s.handlePoliciesActive))
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
