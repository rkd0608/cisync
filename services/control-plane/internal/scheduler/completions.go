package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// completionConsumer is the I-12 dedupe consumer name for the fleet
// completion feed.
const completionConsumer = "completion_ingest"

// IngestCompletions polls the fleet's accepted-terminal feed and drives, per
// completion: validation.completed → evidence evaluation (succeeded) or
// failure classification + routing (failed/timed_out) → decision rendering
// when plan sufficiency reaches 1.
func (e *EngineScheduler) IngestCompletions(ctx context.Context) (int, error) {
	jobs, err := e.fleet.Completed(ctx, 50)
	if err != nil {
		return 0, err
	}
	consumed := 0
	for _, job := range jobs {
		applied, err := e.applyCompletion(ctx, job)
		if err != nil {
			logf("completion %s@%d: %v", job.RunID, job.FenceToken, err)
			continue
		}
		if applied {
			consumed++
		}
	}
	return consumed, nil
}

// applyCompletion runs the full effect pipeline for one completion inside a
// single tx: dedupe first (I-12), then the state machine and projections.
func (e *EngineScheduler) applyCompletion(ctx context.Context, job relay.CompletedJob) (bool, error) {
	run, err := e.store.GetRunByID(ctx, job.RunID)
	if err != nil {
		return false, fmt.Errorf("load run: %w", err)
	}
	if job.FenceToken != run.FenceToken || job.Attempt != run.Attempt {
		// Stale epoch (reclaimed/cancelled elsewhere): diagnostics only —
		// never mutate state from a stale fence holder (I-11).
		logf("stale completion %s@%d (ctrl fence %d); discarded",
			job.RunID, job.FenceToken, run.FenceToken)
		return false, nil
	}
	var applied bool
	err = e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		dedupeKey := fmt.Sprintf("fleet:%s:%d", job.RunID, job.FenceToken)
		first, err := store.MarkProcessedTx(ctx, tx, completionConsumer, dedupeKey)
		if err != nil {
			return err
		}
		if !first {
			return nil // replayed feed row; effects already applied (I-12)
		}
		seq, err := e.appendCompletedTx(ctx, tx, run, job)
		if err != nil {
			return err
		}
		switch job.Status {
		case "succeeded":
			err = e.onRunSucceeded(ctx, tx, run, job, seq)
		case "failed", "timed_out":
			err = e.onRunFailed(ctx, tx, run, job, seq)
		default:
			err = nil // cancelled completions are supersede echoes; no effects
		}
		if err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// appendCompletedTx appends the validation.completed event and advances the
// run projection to its terminal state.
func (e *EngineScheduler) appendCompletedTx(ctx context.Context, tx pgx.Tx, run *domain.ValidationRun, job relay.CompletedJob) (int64, error) {
	trigger := "run." + mapFleetStatus(job.Status)
	if err := run.Apply(trigger); err != nil {
		return 0, fmt.Errorf("advance run: %w", err)
	}
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	ev, err := domain.NewEvent(run.TenantID, //nolint
		domain.AggregateRef{Type: string(domain.AggRun), ID: run.ID},
		"validation.completed", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"run_id":                 run.ID,
			"attempt":                job.Attempt,
			"status":                 job.Status,
			"logs_digest":            job.LogsDigest,
			"artifact_digests":       toAnySlice(job.ArtifactDigests),
			"duration_ms":            job.DurationMS,
			"actual_cost_millicents": job.CostMillicents,
		})
	if err != nil {
		return 0, err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx,
		`UPDATE ctrl.validation_runs SET state=$3, fence_token=$4, finished_at=now(), seq=$5
		 WHERE id=$1 AND tenant_id=$2 AND state <> 'cancelled'`,
		run.ID, run.TenantID, string(run.State), job.FenceToken, ev.Seq)
	if err != nil {
		return 0, fmt.Errorf("update run projection: %w", err)
	}
	return ev.Seq, nil
}

func mapFleetStatus(status string) string {
	return strings.ToLower(status)
}
