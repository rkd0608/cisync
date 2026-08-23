package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	"sauron.dev/sauron/runner-fleet/internal/store"
)

// digestPattern enforces well-formed content digests (sha256:<64 hex>).
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// CompleteHandler serves POST /internal/fleet/jobs/{run_id}/complete.
type CompleteHandler struct {
	store   store.Store
	metrics *obs.Metrics
	logger  *slog.Logger
	nowFn   func() time.Time
}

// NewCompleteHandler builds the completion gate handler.
func NewCompleteHandler(st store.Store, m *obs.Metrics, logger *slog.Logger, nowFn func() time.Time) *CompleteHandler {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &CompleteHandler{store: st, metrics: m, logger: logger, nowFn: nowFn}
}

type completeRequest struct {
	FenceToken           int64    `json:"fence_token"`
	Status               string   `json:"status"`
	LogsDigest           string   `json:"logs_digest"`
	ArtifactDigests      []string `json:"artifact_digests"`
	DurationMS           int64    `json:"duration_ms"`
	ActualCostMilliCents int64    `json:"actual_cost_millicents"`
}

// ServeHTTP implements the fenced completion protocol:
// 200 {"accepted":true} · 409 {"accepted":false,"reason":"fence_mismatch"|
// "already_accepted"}. Only the current fence-token holder may accept and
// acceptance happens at most once per (run, attempt) — I-11.
func (h *CompleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := r.PathValue("run_id")
	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "malformed complete body")
		return
	}
	switch req.Status {
	case domain.StatusSucceeded, domain.StatusFailed, domain.StatusTimedOut:
	default:
		writeError(w, http.StatusBadRequest, "validation_failed", "status must be succeeded|failed|timed_out")
		return
	}
	if !digestPattern.MatchString(req.LogsDigest) {
		writeError(w, http.StatusBadRequest, "validation_failed", "logs_digest must be sha256:<64 hex>")
		return
	}
	for _, d := range req.ArtifactDigests {
		if !digestPattern.MatchString(d) {
			writeError(w, http.StatusBadRequest, "validation_failed", "artifact digests must be sha256:<64 hex>")
			return
		}
	}
	if req.DurationMS < 0 || req.ActualCostMilliCents < 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "negative duration or cost")
		return
	}

	err := h.store.Complete(r.Context(), runID, store.Completion{
		FenceToken:           req.FenceToken,
		Status:               req.Status,
		LogsDigest:           req.LogsDigest,
		ArtifactDigests:      req.ArtifactDigests,
		DurationMS:           req.DurationMS,
		ActualCostMilliCents: req.ActualCostMilliCents,
	}, h.nowFn())
	switch {
	case err == nil:
		h.metrics.CounterInc("fleet_completions_total", "Accepted job completions", "status", req.Status)
		writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
	case errors.Is(err, domain.ErrAlreadyAccepted):
		fenceConflict(w, "already_accepted")
	case errors.Is(err, domain.ErrFenceMismatch):
		h.logger.Warn("stale fence rejected at complete",
			slog.String("run_id", runID), slog.Int64("presented_token", req.FenceToken))
		fenceConflict(w, "fence_mismatch")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "unknown run")
	default:
		writeError(w, http.StatusInternalServerError, "unavailable", "completion failed")
	}
}
