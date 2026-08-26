package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/store"
)

/**
 * P0-3 / I-06 integration: reservation commits with the queued→dispatched
 * flip; release commits with the terminal transition (completion or
 * cancel). Counters must conserve Σreservations − Σreleases.
 * Skips without TEST_PG_DSN.
 */

func budgetUsed(t *testing.T, st *store.Store, tenantID string) map[store.BudgetKind]int64 {
	t.Helper()
	snap, err := st.BudgetUsageSnapshot(context.Background(), []string{tenantID})
	if err != nil {
		t.Fatalf("budget snapshot: %v", err)
	}
	return snap[tenantID]
}

// resetBudgetCounters zeroes the tenant's I-06 baseline: these tests share
// one DB (and one DevTenant row-set) across the whole suite, and both
// conservation assertions are phrased from zero.
func resetBudgetCounters(t *testing.T, st *store.Store, tenantID string) {
	t.Helper()
	if _, err := st.Pool.Exec(context.Background(),
		`DELETE FROM ctrl.budget_counters WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("reset counters: %v", err)
	}
}

func TestDispatchReservesAndCompletionReleasesBudget(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("i06flow-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	resetBudgetCounters(t, st, tenantID)

	before := budgetUsed(t, st, tenantID)[store.BudgetCPUMinutes]

	// Dispatch all three required-kind runs: est 100ms ⇒ 1 cpu-minute per
	// run and one concurrency slot each.
	dispatchRuns(t, engine, seeded.runIDs)
	reserved := budgetUsed(t, st, tenantID)
	wantCPU := int64(len(seeded.runIDs))
	if got := reserved[store.BudgetCPUMinutes] - before; got != wantCPU {
		t.Fatalf("dispatch must reserve cpu_minutes=%d, reserved %d", wantCPU, got-before)
	}
	if reserved[store.BudgetConcurrentCandidates] != wantCPU {
		t.Fatalf("dispatch must reserve one concurrency slot per run (%d), got %d",
			wantCPU, reserved[store.BudgetConcurrentCandidates])
	}

	// Complete one run with actual duration 1000ms ⇒ releases 1 minute +
	// its concurrency slot inside the effect tx.
	completed := completionWithCensus(seeded.runIDs[0], 1, 1, "succeeded", 12, 12, 0, 0, 0)
	completed.DurationMS = 1000
	engine.fleet = &fakeFleet{completed: []relay.CompletedJob{completed}}
	consumed, err := engine.IngestCompletions(ctx)
	if err != nil || consumed != 1 {
		t.Fatalf("ingest completion: consumed=%d err=%v", consumed, err)
	}
	after := budgetUsed(t, st, tenantID)
	if got := after[store.BudgetCPUMinutes]; got != before+wantCPU-1 {
		t.Fatalf("completion must release actual minutes: used=%d want=%d", got, before+wantCPU-1)
	}
	if after[store.BudgetConcurrentCandidates] != wantCPU-1 {
		t.Fatalf("concurrency slot must be released, used=%d", after[store.BudgetConcurrentCandidates])
	}
}

func TestCancelOfDispatchedRunsReleasesReservations(t *testing.T) {
	engine, st, done := pgScheduler(t)
	defer done()
	ctx := context.Background()
	tenantID := config.DevTenant
	tag := fmt.Sprintf("i06cancel-%d", time.Now().UnixNano())
	seeded := seedValidationCandidate(t, st, tenantID, tag)
	resetBudgetCounters(t, st, tenantID)
	dispatchRuns(t, engine, seeded.runIDs)

	var cancelled []string
	if err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		ids, err := store.CancelRunsForCandidateTx(ctx, tx, st, tenantID, seeded.candidateID, "superseded")
		cancelled = ids
		return err
	}); err != nil {
		t.Fatalf("cancel runs: %v", err)
	}
	if len(cancelled) != len(seeded.runIDs) {
		t.Fatalf("all dispatched runs must cancel, got %v", cancelled)
	}
	after := budgetUsed(t, st, tenantID)
	for _, kind := range []store.BudgetKind{store.BudgetCPUMinutes, store.BudgetConcurrentCandidates} {
		if after[kind] != 0 {
			t.Fatalf("%s must be fully released after cancellation, used=%d", kind, after[kind])
		}
	}
}
