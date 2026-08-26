package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/obs"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// HeartbeatHandler serves POST /internal/fleet/jobs/{run_id}/heartbeat.
type HeartbeatHandler struct {
	store    store.Store
	metrics  *obs.Metrics
	nowFn    func() time.Time
	verifier *joblease.Verifier
}

// NewHeartbeatHandler builds the heartbeat handler. verifier enforces the
// job-lease credential gate (B2/I-04); nil fails closed.
func NewHeartbeatHandler(st store.Store, m *obs.Metrics, nowFn func() time.Time, verifier *joblease.Verifier) *HeartbeatHandler {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &HeartbeatHandler{store: st, metrics: m, nowFn: nowFn, verifier: verifier}
}

type heartbeatRequest struct {
	FenceToken int64 `json:"fence_token"`
}

// ServeHTTP implements the heartbeat protocol: 401 unauthorized lease ·
// 204 fresh · 409 stale fence or non-running state (I-11).
func (h *HeartbeatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := r.PathValue("run_id")
	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "malformed heartbeat body")
		return
	}
	fence := req.FenceToken
	if _, ok := authorizeJobMutation(w, r, h.store, h.verifier, &fence); !ok {
		return
	}
	err := h.store.Heartbeat(r.Context(), runID, req.FenceToken, h.nowFn())
	switch {
	case err == nil:
		h.metrics.CounterInc("fleet_heartbeats_total", "Job heartbeats", "outcome", "accepted")
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "unknown run")
	default:
		h.metrics.CounterInc("fleet_heartbeats_total", "Job heartbeats", "outcome", "stale")
		fenceConflict(w, "fence_mismatch")
	}
}
