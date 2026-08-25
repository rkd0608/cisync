package api

import (
	"context"
	"log/slog"
	"net/http"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// serveLifecycle handles kind=lifecycle: create-or-update ONE check run per
// candidate revision walking queued → in_progress → completed in place
// (plan §4.1). Idempotency-Key is "<candidate_id>:<phase>" (deterministic).
func (h *DecisionsHandler) serveLifecycle(w http.ResponseWriter, ctx context.Context, raw []byte, idemKey string) {
	var env domain.LifecycleEnvelope
	if err := jsonUnmarshalStrict(raw, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "invalid lifecycle envelope JSON"))
		return
	}
	if err := env.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	wantKey := env.CandidateID + ":" + string(env.Phase)
	if idemKey != wantKey {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "Idempotency-Key must equal candidate_id:phase"))
		return
	}
	phase, err := env.Phase.GitHubPhase()
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

	// Terminal revisions ignore late phase pushes; a repeated same-phase
	// push is a relay replay, not new state.
	if rec.Phase == domain.PhaseCompleted || rec.Phase == phase {
		h.deps.Metrics.CounterInc("conn_lifecycle_replayed_total", "Lifecycle pushes collapsed as replays", "reason", replayReason(rec.Phase))
		h.respond(w, http.StatusOK, pushResponse{Accepted: true, Replay: true})
		return
	}

	payload, err := checks.RenderLifecycle(&env, h.deps.DetailsURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	result, ok := h.publish(w, ctx, rec, payload)
	if !ok {
		return
	}

	if saveErr := h.deps.Tracker.RecordCheckReport(ctx, tracking.Record{
		CandidateID: env.CandidateID,
		HeadSHA:     env.HeadSHA,
		Repo:        env.Repo,
		CheckRunID:  result.CheckRunID,
		Phase:       phase,
	}); saveErr != nil {
		h.logger.Error("revision persist failed", slog.String("err", saveErr.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}
	h.deps.Metrics.CounterInc("conn_lifecycle_total", "Lifecycle check transitions published", "phase", string(phase))
	h.respond(w, http.StatusAccepted, pushResponse{Accepted: true, Queued: result.Queued, DryRun: result.DryRun})
}

func replayReason(current domain.CheckPhase) string {
	if current == domain.PhaseCompleted {
		return "terminal"
	}
	return "same_phase"
}
