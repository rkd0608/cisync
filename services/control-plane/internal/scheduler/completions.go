package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// errPermanentCompletion marks typed not-found conditions (unknown run,
// plan, candidate or intent) that can never succeed on retry; such rows are
// absorbed as processed diagnostics instead of poisoning the feed (P1-4).
var errPermanentCompletion = errors.New("permanent completion failure")

func permanentCompletion(cause error) error {
	return fmt.Errorf("%w: %w", errPermanentCompletion, cause)
}

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
	// Advisory bulk pre-check so replayed feed rows skip the load+tx pipeline;
	// the authoritative I-12 gate stays inside applyCompletion's tx.
	keys := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keys = append(keys, dedupeKey(job.RunID, job.FenceToken))
	}
	doneKeys, err := e.store.ProcessedKeys(ctx, completionConsumer, keys)
	if err != nil {
		return 0, err
	}
	consumed := 0
	for _, job := range jobs {
		key := dedupeKey(job.RunID, job.FenceToken)
		if doneKeys[key] {
			continue
		}
		applied, err := e.applyCompletion(ctx, job)
		if err == nil {
			if applied {
				consumed++
			}
			continue
		}
		if errors.Is(err, errPermanentCompletion) {
			// P1-4: typed permanent failures are absorbed exactly like the
			// unknown-run path so the feed row stops re-surfacing.
			logf("completion %s@%d absorbed permanently: %v", job.RunID, job.FenceToken, err)
			if absorbErr := e.absorbCompletionRow(ctx, key); absorbErr != nil {
				return consumed, absorbErr
			}
			continue
		}
		attempts, attemptErr := e.recordPoisonAttempt(ctx, key)
		if attemptErr != nil {
			return consumed, fmt.Errorf("poison tracking for %s: %w", key, attemptErr)
		}
		if attempts >= store.MaxFeedAttempts {
			logf("completion %s@%d absorbed after %d failed ticks (poison cap)",
				job.RunID, job.FenceToken, attempts)
			if absorbErr := e.absorbCompletionRow(ctx, key); absorbErr != nil {
				return consumed, absorbErr
			}
			continue
		}
		// Capped backoff skip: retry on a later tick, bounded by the cap.
		logf("completion %s@%d deferred (attempt %d/%d): %v",
			job.RunID, job.FenceToken, attempts, store.MaxFeedAttempts, err)
	}
	return consumed, nil
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
			"results_census":         censusPayload(job.Results),
			// Correlation stamp so the served lifecycle of ONE candidate is
			// traceable through the public tail (ARCHITECTURE_DRAFT §3a).
			"candidate_id": run.CandidateID,
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

// censusPayload renders the outcome census for the ledger event payload so
// served events expose the REAL executed outcome distribution (P0-2/I-01).
func censusPayload(r *relay.CompletedResults) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"total": r.Total, "passed": r.Passed, "failed": r.Failed,
		"skipped": r.Skipped, "quarantined": r.Quarantined,
	}
}

func mapFleetStatus(status string) string {
	return strings.ToLower(status)
}

// dedupeKey is the I-12 consumer key for one fleet completion.
func dedupeKey(runID string, fenceToken int64) string {
	return fmt.Sprintf("fleet:%s:%d", runID, fenceToken)
}
