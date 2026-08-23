package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// defaultCompletedLimit caps the completion feed page size.
const defaultCompletedLimit = 100

// CompletedJobView is one accepted terminal job in the control-plane feed.
type CompletedJobView struct {
	RunID           string   `json:"run_id"`
	Attempt         int      `json:"attempt"`
	FenceToken      int64    `json:"fence_token"`
	Tier            int      `json:"tier"`
	Pool            string   `json:"pool"`
	Status          string   `json:"status"`
	LogsDigest      string   `json:"logs_digest"`
	LogsExcerpt     string   `json:"logs_excerpt,omitempty"`
	ArtifactDigests []string `json:"artifact_digests"`
	DurationMS      int64    `json:"duration_ms"`
	CostMillicents  int64    `json:"actual_cost_millicents"`
	Classification  string   `json:"classification,omitempty"`
}

// CompletedHandler serves GET /internal/fleet/jobs/completed: the pull-based
// completion feed. Control-plane dedupes by (run_id, fence_token) inside its
// effect tx (I-12), so replays of this feed are harmless.
type CompletedHandler struct {
	store  fstore.Store
	logger *slog.Logger
}

// NewCompletedHandler builds the completed-jobs feed handler.
func NewCompletedHandler(st fstore.Store, logger *slog.Logger) *CompletedHandler {
	return &CompletedHandler{store: st, logger: logger}
}

// ServeHTTP implements GET /internal/fleet/jobs/completed?limit=N.
func (h *CompletedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := defaultCompletedLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > 1000 {
			http.Error(w, `{"error":{"code":"validation_failed","message":"limit must be 1..1000"}}`, http.StatusBadRequest)
			return
		}
		limit = n
	}
	jobs, err := h.store.TerminalAccepted(r.Context(), limit)
	if err != nil {
		h.logger.Error("terminal jobs query failed", slog.String("err", err.Error()))
		http.Error(w, `{"error":{"code":"unavailable","message":"storage unavailable"}}`, http.StatusServiceUnavailable)
		return
	}
	out := make([]CompletedJobView, 0, len(jobs))
	for _, j := range jobs {
		view := CompletedJobView{
			RunID:          j.RunID,
			Attempt:        j.Attempt,
			FenceToken:     j.FenceToken,
			Tier:           j.Tier,
			Pool:           j.Pool,
			Status:         j.Status,
			DurationMS:     durationOf(j.ResultRef),
			CostMillicents: costOf(j.ResultRef),
			Classification: classOf(j.ResultRef),
		}
		if v, ok := j.ResultRef["logs_digest"].(string); ok {
			view.LogsDigest = v
		}
		if v, ok := j.ResultRef["logs_excerpt"].(string); ok {
			view.LogsExcerpt = v
		}
		if raw, ok := j.ResultRef["artifact_digests"].([]any); ok {
			for _, d := range raw {
				if s, ok := d.(string); ok {
					view.ArtifactDigests = append(view.ArtifactDigests, s)
				}
			}
		}
		if view.ArtifactDigests == nil {
			view.ArtifactDigests = []string{}
		}
		out = append(out, view)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": out})
}

func durationOf(ref map[string]any) int64 {
	if v, ok := ref["duration_ms"].(float64); ok {
		return int64(v)
	}
	return 0
}

func costOf(ref map[string]any) int64 {
	if v, ok := ref["cost_millicents"].(float64); ok {
		return int64(v)
	}
	return 0
}

func classOf(ref map[string]any) string {
	if v, ok := ref["class"].(string); ok {
		return v
	}
	return ""
}
