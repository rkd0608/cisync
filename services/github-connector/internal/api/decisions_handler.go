// Package api holds the connector HTTP surface (stdlib ServeMux only).
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"cisync.dev/cisync/github-connector/internal/checks"
	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/emit"
	"cisync.dev/cisync/github-connector/internal/obs"
	"cisync.dev/cisync/github-connector/internal/rerun"
	"cisync.dev/cisync/github-connector/internal/tracking"
)

// maxDecisionBodyBytes caps the §4 push payload.
const maxDecisionBodyBytes = int64(1 << 20)

// CheckEmitter is the publication + resolution surface the flows need
// (satisfied by *emit.Router; tests record instead).
type CheckEmitter interface {
	Create(ctx context.Context, repo string, payload checks.CheckPayload) (emit.Result, error)
	Update(ctx context.Context, repo string, checkRunID int64, payload checks.CheckPayload) (emit.Result, error)
	InstallationFor(ctx context.Context, repo string) (installationID int64, ok bool)
}

// HandlerDeps bundles the collaborators of the §4 push endpoint.
type HandlerDeps struct {
	Tracker      tracking.Store
	Router       CheckEmitter
	Metrics      *obs.Metrics
	DetailsURL   string
	RerunPolicy  rerun.Policy
	RerunBudget  *rerun.Budget
	RerunControl *rerun.Control
	RerunSeen    *rerun.Dedupe
	Now          func() time.Time
}

// DecisionsHandler terminates POST /internal/connector/decisions (widened
// internal-protocols §4): HMAC verify → kind dispatch → validate →
// idempotency → per-kind flow. One door, three envelope kinds.
type DecisionsHandler struct {
	deps   HandlerDeps
	logger *slog.Logger
	secret []byte
}

// NewDecisionsHandler wires the §4 push endpoint.
func NewDecisionsHandler(secret string, deps HandlerDeps, logger *slog.Logger) *DecisionsHandler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &DecisionsHandler{deps: deps, logger: logger, secret: []byte(secret)}
}

// pushResponse is the typed accepted/replay body for every envelope kind.
type pushResponse struct {
	Accepted bool   `json:"accepted"`
	DryRun   bool   `json:"dry_run"`
	Replay   bool   `json:"replay,omitempty"`
	Queued   bool   `json:"queued,omitempty"`
	Outcome  string `json:"outcome,omitempty"` // rerun outcomes only
}

// ServeHTTP implements the widened §4 protocol:
// 202 accepted · 200 replay · 400 validation_failed · 401 bad signature ·
// 404 unknown_candidate (rerun) · 413 too large · 503 unavailable.
func (h *DecisionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := readBody(w, r.Body, maxDecisionBodyBytes)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody("payload_too_large", "payload exceeds size cap"))
		return
	}
	if !VerifyHMAC(h.secret, raw, r.Header.Get("X-CISync-Signature")) {
		h.deps.Metrics.CounterInc("conn_decisions_rejected_total", "Decision pushes rejected at the boundary", "reason", "bad_signature")
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "bad signature"))
		return
	}
	var peek struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "invalid envelope JSON"))
		return
	}
	kind, err := domain.KindFor(peek.Kind)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	idemKey := r.Header.Get("Idempotency-Key")
	switch kind {
	case domain.KindDecision:
		h.serveDecision(w, r.Context(), raw, idemKey)
	case domain.KindLifecycle:
		h.serveLifecycle(w, r.Context(), raw, idemKey)
	case domain.KindRerun:
		h.serveRerun(w, r.Context(), raw, idemKey)
	}
}

func (h *DecisionsHandler) respond(w http.ResponseWriter, status int, body pushResponse) {
	h.deps.Metrics.GaugeSet("conn_dry_run_mode", "Whether the connector currently logs instead of publishing",
		boolGauge(body.DryRun))
	writeJSON(w, status, body)
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
