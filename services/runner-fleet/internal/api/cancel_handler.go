package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/execute"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/obs"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// CancelHandler serves POST /internal/fleet/jobs/{run_id}/cancel.
type CancelHandler struct {
	store    store.Store
	registry *execute.Registry
	provider domain.Provider
	metrics  *obs.Metrics
	logger   *slog.Logger
	nowFn    func() time.Time
	verifier *joblease.Verifier
}

// NewCancelHandler builds the cancel handler; registry provides best-effort
// provider cancellation for in-flight jobs. verifier enforces the job-lease
// credential gate (B2/I-04); nil fails closed.
func NewCancelHandler(st store.Store, reg *execute.Registry, p domain.Provider, m *obs.Metrics, logger *slog.Logger, nowFn func() time.Time, verifier *joblease.Verifier) *CancelHandler {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &CancelHandler{store: st, registry: reg, provider: p, metrics: m, logger: logger, nowFn: nowFn, verifier: verifier}
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

// ServeHTTP implements idempotent cancellation: 204 always for known runs.
// Cancelling a terminal job is a no-op; cancelling running/queued work bumps
// the epoch so any late result from the previous holder is rejected (I-11).
func (h *CancelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := r.PathValue("run_id")
	var req cancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "malformed cancel body")
		return
	}

	if _, ok := authorizeJobMutation(w, r, h.store, h.verifier, nil); !ok {
		return
	}
	job, err := h.store.Get(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "unknown run")
		return
	}
	if domain.Terminal(job.Status) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	cancelled, err := h.store.Cancel(r.Context(), runID, req.Reason, h.nowFn())
	if err != nil {
		h.logger.Error("cancel failed", slog.String("run_id", runID), slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "unavailable", "cancel failed")
		return
	}
	if !cancelled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if handle, ok := h.registry.Lookup(runID); ok {
		if err := h.provider.Cancel(handle); err != nil {
			h.logger.Warn("best-effort provider cancel failed",
				slog.String("run_id", runID), slog.String("err", err.Error()))
		}
	}
	h.metrics.CounterInc("fleet_cancellations_total", "Job cancellations")
	w.WriteHeader(http.StatusNoContent)
}
