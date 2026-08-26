package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
	"sauron.dev/sauron/ingest/internal/forward"
)

// hook_lifecycle.go carries the per-delivery forward attempt and its
// outcome persistence, split from github_hook_handler.go for the charter's
// 250-line file cap.

// forwardOne performs a single redacted, signed forward attempt. The payload
// is scrubbed BEFORE anything leaves the process (T1).
func (h *GitHubHookHandler) forwardOne(r *http.Request, d domain.Delivery) (forward.Result, error) {
	clean, err := forward.RedactPayload(d.Payload)
	if err != nil {
		return forward.ResultRejected, err
	}
	env := forward.Envelope{
		Source:           d.Source,
		ExtDeliveryID:    d.ExtDeliveryID,
		EventKind:        d.EventKind,
		Repo:             d.Repo,
		ReceivedAt:       d.ReceivedAt.UTC(),
		Payload:          json.RawMessage(clean),
		DuplicateSuspect: d.DuplicateSuspect,
	}
	return h.forwarder.Send(r.Context(), env)
}

// recordOutcome persists the single source of truth for forward state.
func (h *GitHubHookHandler) recordOutcome(ctx context.Context, d domain.Delivery, result forward.Result, cause error) {
	now := h.nowFn()
	var status string
	attempts := d.Attempts
	var forwardedAt time.Time
	switch result {
	case forward.ResultAccepted:
		status = domain.StatusForwarded
		forwardedAt = now
	case forward.ResultUnavailable:
		status = domain.StatusPending
		attempts++
	default:
		status = domain.StatusForwardFailed
		attempts++
	}
	err := h.store.UpdateForwardState(ctx, d.ID, status, attempts, now, forwardedAt)
	if err != nil {
		h.logger.Error("update delivery state failed",
			slog.String("delivery_id", d.ID), slog.String("err", err.Error()))
	}
	if cause != nil && status != domain.StatusForwarded {
		h.logger.Warn("delivery forward incomplete",
			slog.String("delivery_id", d.ID), slog.String("status", status), slog.String("err", cause.Error()))
	}
}
