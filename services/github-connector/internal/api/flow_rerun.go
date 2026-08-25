package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/rerun"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// serveRerun handles kind=rerun_requested (plan §4.5): policy + budget
// guardrails decide between replan (ctrl revalidate), replay_cached, or a
// visible neutral decline. A required check never silently ignores a run.
func (h *DecisionsHandler) serveRerun(w http.ResponseWriter, ctx context.Context, raw []byte, idemKey string) {
	var env domain.RerunEnvelope
	if err := jsonUnmarshalStrict(raw, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "invalid rerun envelope JSON"))
		return
	}
	if err := env.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	if idemKey == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_failed", "Idempotency-Key (originating ext_delivery_id) required"))
		return
	}
	if !h.deps.RerunSeen.FirstSeen(idemKey) {
		h.respond(w, http.StatusOK, pushResponse{Accepted: true, Replay: true})
		return
	}

	rec, err := h.deps.Tracker.LookupCheckReport(ctx, env.CandidateID, env.HeadSHA)
	switch {
	case err == tracking.ErrNotFound:
		h.deps.Metrics.CounterInc("conn_rerun_total", "Re-run requests handled", "outcome", "unknown_candidate")
		writeJSON(w, http.StatusNotFound, errorBody("unknown_candidate", "no tracked revision for candidate"))
		return
	case err != nil:
		h.logger.Error("revision lookup failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, errorBody("unavailable", "storage unavailable; redeliver"))
		return
	}

	instID, _ := h.deps.Router.InstallationFor(ctx, env.Repo)
	if verdict := h.deps.RerunBudget.Allow(env.CandidateID, instID); !verdict.Allowed {
		h.declineRerun(w, ctx, rec, true)
		return
	}

	switch h.deps.RerunPolicy {
	case rerun.PolicyReplayCached:
		h.replayCached(w, ctx, rec)
	default:
		h.replan(w, ctx, rec, instID, idemKey)
	}
}

// replan flips the check back to queued after control-plane accepts the
// re-plan command; the originating ext_delivery_id rides as Idempotency-Key.
// Unreachable ctrl ⇒ neutral "unavailable"; ctrl 409 budget exhaustion ⇒
// neutral "budget exhausted" (permanent verdict, §4.4).
func (h *DecisionsHandler) replan(w http.ResponseWriter, ctx context.Context, rec *tracking.Record, instID int64, idemKey string) {
	err := h.deps.RerunControl.Revalidate(ctx, rec.CandidateID, idemKey)
	if err == nil {
		h.deps.RerunBudget.Record(rec.CandidateID, instID)
		queued := domain.LifecycleEnvelope{
			Kind: domain.KindLifecycle, Phase: domain.LifecycleQueued,
			CandidateID: rec.CandidateID, Repo: rec.Repo, HeadSHA: rec.HeadSHA,
			At: h.deps.Now(),
		}
		payload, renderErr := checks.RenderLifecycle(&queued, h.deps.DetailsURL)
		if renderErr == nil {
			if _, ok := h.publish(w, ctx, rec, payload); ok {
				_ = h.deps.Tracker.RecordCheckReport(ctx, tracking.Record{
					CandidateID: rec.CandidateID, HeadSHA: rec.HeadSHA, Repo: rec.Repo,
					CheckRunID: rec.CheckRunID, Phase: domain.PhaseQueued,
					LastDecision: rec.LastDecision,
				})
				h.rerunOutcome(w, ctx, "replanned")
			}
			return
		}
	}
	var unknown *rerun.ErrUnknownCandidate
	if errors.As(err, &unknown) {
		h.deps.Metrics.CounterInc("conn_rerun_total", "Re-run requests handled", "outcome", "unknown_candidate")
		writeJSON(w, http.StatusNotFound, errorBody("unknown_candidate", "control-plane has no such candidate"))
		return
	}
	var exhausted *rerun.ErrBudgetExhausted
	if errors.As(err, &exhausted) {
		h.declineRerun(w, ctx, rec, true)
		return
	}
	h.logger.Warn("replan unavailable; declining rerun neutrally",
		slog.String("candidate_id", rec.CandidateID), slog.String("err", err.Error()))
	h.declineRerun(w, ctx, rec, false)
}

// replayCached republishes the stored last decision with the frozen cached
// marker — zero recompute (plan §4.5).
func (h *DecisionsHandler) replayCached(w http.ResponseWriter, ctx context.Context, rec *tracking.Record) {
	if rec.LastDecision == nil {
		h.rerunOutcome(w, ctx, "no_cached_decision")
		return
	}
	payload, err := checks.RenderCached(rec.LastDecision, h.deps.DetailsURL)
	if err != nil {
		h.rerunOutcome(w, ctx, "render_failed")
		return
	}
	if _, ok := h.publish(w, ctx, rec, payload); !ok {
		return
	}
	_ = h.deps.Tracker.RecordCheckReport(ctx, tracking.Record{
		CandidateID: rec.CandidateID, HeadSHA: rec.HeadSHA, Repo: rec.Repo,
		CheckRunID: rec.CheckRunID, Phase: domain.PhaseCompleted,
		Conclusion: payload.Conclusion, DecisionID: rec.DecisionID,
		LastDecision: rec.LastDecision,
	})
	h.rerunOutcome(w, ctx, "replayed_cached")
}

// declineRerun flips the check to neutral so the re-runner sees an explicit
// verdict instead of a stranded yellow gate.
func (h *DecisionsHandler) declineRerun(w http.ResponseWriter, ctx context.Context, rec *tracking.Record, exhausted bool) {
	outcome := "unavailable"
	if exhausted {
		outcome = "exhausted"
	}
	payload := checks.RenderRerunDeclined(rec.CandidateID, rec.HeadSHA, h.deps.DetailsURL, h.deps.Now(), exhausted)
	if _, ok := h.publish(w, ctx, rec, payload); !ok {
		return
	}
	_ = h.deps.Tracker.RecordCheckReport(ctx, tracking.Record{
		CandidateID: rec.CandidateID, HeadSHA: rec.HeadSHA, Repo: rec.Repo,
		CheckRunID: rec.CheckRunID, Phase: domain.PhaseCompleted,
		Conclusion: payload.Conclusion, DecisionID: rec.DecisionID,
		LastDecision: rec.LastDecision,
	})
	h.rerunOutcome(w, ctx, outcome)
}

func (h *DecisionsHandler) rerunOutcome(w http.ResponseWriter, ctx context.Context, outcome string) {
	h.deps.Metrics.CounterInc("conn_rerun_total", "Re-run requests handled", "outcome", outcome)
	h.respond(w, http.StatusAccepted, pushResponse{Accepted: true, Outcome: outcome})
}
