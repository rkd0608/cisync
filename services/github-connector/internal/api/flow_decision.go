package api

import (
	"context"
	"log/slog"
	"net/http"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// serveDecision handles kind=decision: dedupe by decision_id → render the
// completed payload → create-or-update the revision's check run → record.
func (h *DecisionsHandler) serveDecision(w http.ResponseWriter, ctx context.Context, raw []byte, idemKey string) {
	var env domain.DecisionEnvelope
	if err := jsonUnmarshalStrict(raw, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "invalid decision envelope JSON"))
		return
	}
	if err := env.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	if idemKey != env.DecisionID {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "Idempotency-Key must equal decision_id"))
		return
	}

	if _, err := h.deps.Tracker.FindByDecision(ctx, env.DecisionID); err == nil {
		h.deps.Metrics.CounterInc("conn_decisions_deduped_total", "Duplicate decision pushes collapsed")
		h.respond(w, http.StatusOK, pushResponse{Accepted: true, Replay: true})
		return
	} else if err != tracking.ErrNotFound {
		h.logger.Error("decision lookup failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}

	payload, err := checks.RenderDecision(&env, h.deps.DetailsURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}

	rec, err := h.deps.Tracker.LookupCheckReport(ctx, env.CandidateID, env.HeadSHA)
	switch {
	case err == tracking.ErrNotFound:
		rec = &tracking.Record{CandidateID: env.CandidateID, HeadSHA: env.HeadSHA, Repo: env.Repo}
	case err != nil:
		h.logger.Error("revision lookup failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}

	result, ok := h.publish(w, ctx, rec, payload)
	if !ok {
		return // publish() already wrote the response
	}

	if saveErr := h.deps.Tracker.RecordCheckReport(ctx, tracking.Record{
		CandidateID:  env.CandidateID,
		HeadSHA:      env.HeadSHA,
		Repo:         env.Repo,
		CheckRunID:   result.CheckRunID,
		Phase:        domain.PhaseCompleted,
		Conclusion:   payload.Conclusion,
		DecisionID:   env.DecisionID,
		LastDecision: &env,
	}); saveErr != nil && saveErr != tracking.ErrNotFound {
		h.logger.Error("check report persist failed", slog.String("err", saveErr.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}
	h.deps.Metrics.CounterInc("conn_checks_rendered_total", "Agent Verification Gate checks rendered",
		"conclusion", payload.Conclusion)
	h.respond(w, http.StatusAccepted, pushResponse{Accepted: true, Queued: result.Queued, DryRun: result.DryRun})
}

// publish performs the create-or-update against the tracked check run and
// centralizes the failure response shape.
func (h *DecisionsHandler) publish(w http.ResponseWriter, ctx context.Context, rec *tracking.Record, payload checks.CheckPayload) (emit.Result, bool) {
	var (
		result emit.Result
		err    error
	)
	if rec.CheckRunID > 0 {
		result, err = h.deps.Router.Update(ctx, rec.Repo, rec.CheckRunID, payload)
	} else {
		result, err = h.deps.Router.Create(ctx, rec.Repo, payload)
	}
	if err != nil {
		h.logger.Error("check publish failed",
			slog.String("candidate_id", rec.CandidateID), slog.String("err", err.Error()))
		writeJSON(w, http.StatusBadGateway, errorBody("upstream_error", "github check publication failed"))
		return emit.Result{}, false
	}
	return result, true
}
