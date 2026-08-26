package scheduler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/store"
)

// absorbCompletionRow marks one feed key processed inside its own tx so it
// never re-surfaces; used for diagnostics and poison-cap absorption.
func (e *EngineScheduler) absorbCompletionRow(ctx context.Context, key string) error {
	return e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.MarkProcessedTx(ctx, tx, completionConsumer, key); err != nil {
			return err
		}
		return store.ClearFeedRetriesTx(ctx, tx, completionConsumer, []string{key})
	})
}

// recordPoisonAttempt counts one failed tick outside any effect tx — the
// count is advisory bookkeeping, safe to persist independently.
func (e *EngineScheduler) recordPoisonAttempt(ctx context.Context, key string) (int, error) {
	var attempts int
	err := e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		n, err := store.RecordFeedFailureTx(ctx, tx, completionConsumer, key)
		attempts = n
		return err
	})
	return attempts, err
}

// applyCompletion runs the full effect pipeline for one completion inside a
// single tx: dedupe first (I-12), then the state machine and projections.
func (e *EngineScheduler) applyCompletion(ctx context.Context, job relay.CompletedJob) (bool, error) {
	run, err := e.store.GetRunByID(ctx, job.RunID)
	if err != nil {
		if err == domain.ErrNotFound {
			// Unknown run (e.g. a probe job seeded straight into the fleet):
			// nothing to mutate — absorb as a marked diagnostic so the feed
			// row stops re-surfacing every tick.
			logf("unknown-run completion %s@%d absorbed; no effects", job.RunID, job.FenceToken)
			if err := e.absorbCompletionRow(ctx, dedupeKey(job.RunID, job.FenceToken)); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, fmt.Errorf("load run: %w", err)
	}
	if job.FenceToken != run.FenceToken || job.Attempt != run.Attempt {
		// Stale epoch (reclaimed/cancelled elsewhere): never mutate state
		// from a stale fence holder (I-11). B7: this rejection is a
		// security-audit event. WHY absorbed-as-processed: the §4 feed
		// re-presents accepted rows forever, so without absorption the
		// stale row would re-audit every tick; marking it processed gives
		// the audit emission natural exactly-once semantics (I-12) while
		// leaving the run's CURRENT epoch free to complete normally.
		logf("stale completion %s@%d (ctrl fence %d); discarded",
			job.RunID, job.FenceToken, run.FenceToken)
		return false, e.absorbStaleCompletion(ctx, run, job)
	}
	// Decision freshness: a completion for an already-terminal run or a
	// decided candidate is absorbed as a diagnostic (I-08) and marked
	// processed so the feed row stops re-surfacing every tick.
	candState, err := e.store.CandidateStateByID(ctx, run.TenantID, run.CandidateID)
	if err != nil && err != domain.ErrNotFound {
		return false, fmt.Errorf("load candidate state: %w", err)
	}
	if reason := completionIsDiagnostic(run.State, candState); reason {
		logf("diagnostic completion %s@%d absorbed (run %s, candidate %s); no effects",
			job.RunID, job.FenceToken, run.State, candState)
		if err := e.store.ExecTx(ctx, func(tx pgx.Tx) error {
			_, err := store.MarkProcessedTx(ctx, tx, completionConsumer,
				dedupeKey(job.RunID, job.FenceToken))
			return err
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	var applied bool
	err = e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		dedupeKey := dedupeKey(job.RunID, job.FenceToken)
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
		// I-06: release the reservation in the SAME tx as the terminal
		// flip — actual cpu minutes where measurable, the estimate as
		// fallback; every dispatched run held one concurrency slot.
		if err := store.ReleaseBudgetsTx(ctx, tx, run.TenantID, seq,
			store.BudgetDeltas{
				store.BudgetCPUMinutes:           store.ActualCPUMinutes(job.DurationMS, run.EstDurationMS),
				store.BudgetConcurrentCandidates: 1,
			}); err != nil {
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
		if err := store.ClearFeedRetriesTx(ctx, tx, completionConsumer, []string{dedupeKey}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}
