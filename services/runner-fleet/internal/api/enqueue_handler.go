package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// maxEnqueueBodyBytes caps the enqueue request payload.
const maxEnqueueBodyBytes = int64(1 << 20)

// EnqueueRequest is one job pushed by control-plane before dispatch
// (internal-protocols §2 extension): the fleet-side execution_jobs row that
// makes the run claimable. LeaseToken is the Ed25519 job-lease JWT minted at
// dispatch (B2); synthetic probe jobs may omit it, but then every mutation
// on the job is rejected as unauthorized.
type EnqueueRequest struct {
	RunID      string         `json:"run_id"`
	Attempt    int            `json:"attempt"`
	Tier       int            `json:"tier"`
	Pool       string         `json:"pool"`
	JobSpec    domain.JobSpec `json:"job_spec"`
	LeaseToken string         `json:"lease_token,omitempty"`
}

// EnqueueHandler serves POST /internal/fleet/jobs: idempotent insert of a
// queued execution job (duplicate run_id is accepted as success so retries
// of the dispatch path are harmless).
type EnqueueHandler struct {
	store  fstore.Store
	logger *slog.Logger
	nowFn  func() time.Time
}

// NewEnqueueHandler builds the enqueue handler; nowFn injectable for tests.
func NewEnqueueHandler(st fstore.Store, logger *slog.Logger, nowFn func() time.Time) *EnqueueHandler {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &EnqueueHandler{store: st, logger: logger, nowFn: nowFn}
}

// ServeHTTP implements POST /internal/fleet/jobs.
func (h *EnqueueHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
		if int64(len(raw)) > maxEnqueueBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "enqueue body exceeds cap")
			return
		}
	}
	var req EnqueueRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "malformed enqueue body")
		return
	}
	if req.RunID == "" || req.Pool == "" || req.JobSpec.Kind == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "run_id, pool and job_spec.kind are required")
		return
	}
	if req.Attempt <= 0 {
		req.Attempt = 1
	}
	err := h.store.Enqueue(r.Context(), domain.Job{
		RunID:      req.RunID,
		Attempt:    req.Attempt,
		Tier:       req.Tier,
		Pool:       req.Pool,
		Spec:       req.JobSpec,
		LeaseToken: req.LeaseToken,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
	case errors.Is(err, domain.ErrDuplicateRun):
		writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "replay": true})
	default:
		h.logger.Error("enqueue failed", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "unavailable", "enqueue failed")
	}
}
