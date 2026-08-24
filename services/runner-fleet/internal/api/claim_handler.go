// Package api implements the runner-fleet worker protocol exactly as defined
// in packages/contracts/internal-protocols.md §2.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/store"
)

// ClaimHandler serves POST /internal/fleet/jobs/claim.
type ClaimHandler struct {
	store   store.Store
	logger  *slog.Logger
	nowFn   func() time.Time
	maxJobs int
}

// NewClaimHandler builds the claim handler; nowFn is injectable for tests.
func NewClaimHandler(st store.Store, logger *slog.Logger, nowFn func() time.Time) *ClaimHandler {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &ClaimHandler{store: st, logger: logger, nowFn: nowFn, maxJobs: 32}
}

type claimRequest struct {
	Pool     string `json:"pool"`
	Limit    int    `json:"limit"`
	WorkerID string `json:"worker_id"`
}

type claimedJob struct {
	RunID      string         `json:"run_id"`
	Attempt    int            `json:"attempt"`
	FenceToken int64          `json:"fence_token"`
	Tier       int            `json:"tier"`
	Pool       string         `json:"pool"`
	JobSpec    domain.JobSpec `json:"job_spec"`
	// LeaseToken hands the dispatch-time credential to the claiming worker;
	// it MUST be presented as Authorization: Bearer on heartbeat/complete
	// (internal-protocols §2, THREAT_MODEL B2).
	LeaseToken string `json:"lease_token,omitempty"`
}

type claimResponse struct {
	Jobs []claimedJob `json:"jobs"`
}

// ServeHTTP implements POST /internal/fleet/jobs/claim. Claims are atomic
// server-side; a run is held by at most one worker at a time.
func (h *ClaimHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "malformed claim body")
		return
	}
	if req.Pool == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "pool is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 4
	}
	if req.Limit > h.maxJobs {
		req.Limit = h.maxJobs
	}
	if req.WorkerID == "" {
		req.WorkerID = "anonymous"
	}
	// The claiming worker is registered OUTSIDE the claim tx (which must
	// never touch fleet.workers — W3 lock-convoy finding) but BEFORE the
	// claim: execution_jobs.claimed_by's FK rejects an unknown worker, so
	// external claims of never-seen worker ids would 500 (live W4 finding).
	if err := h.store.EnsureWorker(r.Context(), req.WorkerID, req.Pool, 1, h.nowFn()); err != nil {
		h.logger.Error("worker registration failed", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "unavailable", "claim failed")
		return
	}
	jobs, err := h.store.ClaimJobs(r.Context(), store.Claim{
		Pool:     req.Pool,
		Provider: "external",
		Limit:    req.Limit,
		WorkerID: req.WorkerID,
	}, h.nowFn())
	if err != nil {
		h.logger.Error("claim failed", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "unavailable", "claim failed")
		return
	}
	resp := claimResponse{Jobs: make([]claimedJob, 0, len(jobs))}
	for _, j := range jobs {
		resp.Jobs = append(resp.Jobs, claimedJob{
			RunID:      j.RunID,
			Attempt:    j.Attempt,
			FenceToken: j.FenceToken,
			Tier:       j.Tier,
			Pool:       j.Pool,
			JobSpec:    j.Spec,
			LeaseToken: j.LeaseToken,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeError(w http.ResponseWriter, code int, errCode string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": errCode, "message": message},
	})
}

func fenceConflict(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"accepted": false,
		"reason":   reason,
	})
}
