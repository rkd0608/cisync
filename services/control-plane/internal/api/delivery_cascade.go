package api

import (
	"context"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/store"
)

// applyPRClosed and the shared cascade helpers live here to keep
// delivery_effects.go under the charter's 250-line cap.

// applyPRClosed cancels live candidates of a closed PR (EC-002: late pushes
// after close create no work).
func (s *Server) applyPRClosed(ctx context.Context, tx pgx.Tx, tenant, extID string, view deliveryView) error {
	intent, err := s.store.IntentForPR(ctx, tenant, view.Repo, view.PR.Number)
	if errorsIsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	reason := "closed_without_merge"
	if view.PR.Merged {
		reason = "merged"
	}
	prior, err := store.LiveCandidatesForIntentTx(ctx, tx, tenant, intent.ID)
	if err != nil {
		return err
	}
	actor := webhookActor("")
	for _, p := range prior {
		cancelEv, err := domain.NewEvent(tenant,
			domain.AggregateRef{Type: string(domain.AggCandidate), ID: p.ID},
			"candidate.cancelled", extID, domain.NewCorrelationID(), actor, map[string]any{
				"candidate_id": p.ID,
				"reason":       reason,
			})
		if err != nil {
			return err
		}
		runIDs, err := store.QueuedRunIDsTx(ctx, tx, tenant, p.ID)
		if err != nil {
			return err
		}
		cancelEvents, err := cancelQueuedRunsEvents(tenant, runIDs, "intent_closed", actor)
		if err != nil {
			return err
		}
		events := append([]*domain.Event{cancelEv}, cancelEvents...)
		if err := s.appendEventsAndProjectCancels(ctx, tx, events, runIDs, nil); err != nil {
			return err
		}
		if err := store.CancelCandidateTx(ctx, tx, tenant, p.ID, cancelEv.Seq); err != nil {
			return err
		}
	}
	return nil
}

// appendEventsAndProjectCancels appends events in ONE batch (chain-lock
// discipline) then flips the cancelled run/repair projection rows.
func (s *Server) appendEventsAndProjectCancels(ctx context.Context, tx pgx.Tx, events []*domain.Event, runIDs, repairIDs []string) error {
	if err := s.store.AppendEventsTx(ctx, tx, events); err != nil {
		return err
	}
	s.metrics.Add("cisync_ctrl_events_appended_total", float64(len(events)))
	for _, runID := range runIDs {
		if _, err := tx.Exec(ctx,
			`UPDATE ctrl.validation_runs SET state='cancelled', finished_at=now() WHERE id=$1 AND state IN ('queued','dispatched')`,
			runID); err != nil {
			return err
		}
	}
	for _, repairID := range repairIDs {
		if _, err := tx.Exec(ctx,
			`UPDATE ctrl.repair_tasks SET state='aborted' WHERE id=$1`, repairID); err != nil {
			return err
		}
	}
	return nil
}

// cancelQueuedRunsEvents builds one validation.cancelled event per run,
// mirroring the reconciler's per-run convention (run_maintenance.go).
func cancelQueuedRunsEvents(tenant string, runIDs []string, reason string, actor domain.EventActor) ([]*domain.Event, error) {
	events := make([]*domain.Event, 0, len(runIDs))
	for _, runID := range runIDs {
		ev, err := domain.NewEvent(tenant,
			domain.AggregateRef{Type: string(domain.AggRun), ID: runID},
			"validation.cancelled", "", domain.NewCorrelationID(), actor,
			map[string]any{"run_ids": []any{runID}, "reason": reason})
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func anyStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}
