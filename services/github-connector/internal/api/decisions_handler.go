package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/store"
)

// maxDecisionBodyBytes caps the decision push payload.
const maxDecisionBodyBytes = int64(1 << 20)

// DecisionsHandler terminates POST /internal/connector/decisions
// (internal-protocols.md §4): HMAC verify → validate → dedupe by
// decision_id → render + publish one GitHub check → persist the report.
type DecisionsHandler struct {
	store     store.Store
	publisher checks.Publisher
	metrics   *obs.Metrics
	logger    *slog.Logger
	details   string
	dryRun    bool
	secret    []byte
}

// NewDecisionsHandler wires the decisions endpoint.
func NewDecisionsHandler(st store.Store, pub checks.Publisher, m *obs.Metrics, logger *slog.Logger, secret string, detailsURL string, dryRun bool) *DecisionsHandler {
	return &DecisionsHandler{
		store:     st,
		publisher: pub,
		metrics:   m,
		logger:    logger,
		secret:    []byte(secret),
		details:   detailsURL,
		dryRun:    dryRun,
	}
}

// ServeHTTP implements the §4 protocol:
// 202 accepted · 200 replay (idempotent) · 401 bad signature ·
// 400 validation_failed · 413 too large · 503 storage unavailable.
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
	if !VerifyHMAC(h.secret, raw, r.Header.Get("X-Sauron-Signature")) {
		h.metrics.CounterInc("conn_decisions_rejected_total", "Decision pushes rejected at the boundary", "reason", "bad_signature")
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "bad signature"))
		return
	}

	var env domain.DecisionEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "invalid decision envelope JSON"))
		return
	}
	if err := env.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	if key := r.Header.Get("Idempotency-Key"); key != env.DecisionID {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "Idempotency-Key must equal decision_id"))
		return
	}

	existing, err := h.store.GetCheckReport(r.Context(), env.DecisionID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, reportBody(existing))
		h.metrics.CounterInc("conn_decisions_deduped_total", "Duplicate decision pushes collapsed")
		return
	case err != store.ErrNotFound:
		h.logger.Error("check report lookup failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}

	payload, err := checks.Render(&env, h.details)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	checkRunID, err := h.publisher.Publish(r.Context(), env.Repo, payload)
	if err != nil {
		h.logger.Error("check publish failed",
			slog.String("decision_id", env.DecisionID), slog.String("err", err.Error()))
		h.metrics.CounterInc("conn_check_publish_failures_total", "GitHub check publications that failed")
		writeJSON(w, http.StatusBadGateway, errorBody("upstream_error", "github check publication failed"))
		return
	}
	rep := store.CheckReport{
		DecisionID:  env.DecisionID,
		CandidateID: env.CandidateID,
		Repo:        env.Repo,
		HeadSHA:     env.HeadSHA,
		Verb:        env.Verb,
		Conclusion:  payload.Conclusion,
		CheckRunID:  checkRunID,
		DryRun:      h.dryRun,
	}
	if err := h.store.SaveCheckReport(r.Context(), rep); err != nil && err != store.ErrDuplicate {
		h.logger.Error("check report persist failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}
	h.metrics.CounterInc("conn_checks_rendered_total", "Agent Verification Gate checks rendered", "conclusion", payload.Conclusion)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "dry_run": h.dryRun})
}

func errorBody(code, message string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code, "message": message}}
}

func reportBody(rep *store.CheckReport) map[string]any {
	return map[string]any{
		"accepted":     true,
		"replay":       true,
		"decision_id":  rep.DecisionID,
		"conclusion":   rep.Conclusion,
		"check_run_id": rep.CheckRunID,
		"dry_run":      rep.DryRun,
	}
}
