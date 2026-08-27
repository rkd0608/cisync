package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/authusers"
	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/domain"
	plannerengine "cisync.dev/cisync/control-plane/internal/planner"
	"cisync.dev/cisync/control-plane/internal/store"
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
	// sessionSigner/Verifier implement the stateless web-session JWTs
	// (email+password auth). Both nil when CISYNC_SESSION_KEY_FILE is unset;
	// /v1/auth/* then fails closed instead of silently accepting.
	sessionSigner   *authusers.Signer
	sessionVerifier *authusers.Verifier
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
				s.metrics.Add("cisync_security_audit_total", 1, "kind", string(kind))
			},
			OnDropped: func(kind audit.Kind) {
				s.metrics.Add("cisync_security_audit_dropped_total", 1, "kind", string(kind))
			},
		})
	// WHY session keys build before serving (not per-request): one PEM parse
	// at boot keeps the hot path allocation-free and surfaces a bad key file
	// as a loud startup error rather than a 500 at first login.
	// (Loaded via UseSessionKey from main after construction.)
	return s
}

// UseSessionKey loads the DEDICATED session-signing Ed25519 key (mirrors
// store.UseSigningKey's late-bind seam). Empty path is allowed in dev:
// /v1/auth/* then fails closed with 503 instead of minting unverifiable
// tokens. Errors are loud — a bad key file must block serving, not degrade.
func (s *Server) UseSessionKey(keyFile string) error {
	if keyFile == "" {
		return nil
	}
	signer, err := authusers.NewSignerFromPEMFile(keyFile)
	if err != nil {
		return err
	}
	s.sessionSigner = signer
	s.sessionVerifier = signer.Verifier()
	return nil
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

// Public email+password auth surface (SPEC §3 2026-08-26): NO admin bearer —
// the browser obtains a session JWT here and carries it as an httpOnly
// cookie through the web gateway. Registered without requireAuth.
mux.HandleFunc("POST /v1/auth/signup", s.handleAuthSignup)
mux.HandleFunc("POST /v1/auth/login", s.handleAuthLogin)
mux.HandleFunc("GET /v1/auth/me", s.requireSession(s.handleAuthMe))

mux.HandleFunc("POST /v1/hooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("POST /internal/ctrl/deliveries", s.handleDelivery)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "resource not found", nil, nil, nil)
		s.metrics.Inc("cisync_ctrl_http_requests_total", "404")
	})
	return mux
}
