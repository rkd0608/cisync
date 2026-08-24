package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// P0-3 windowing regression (live W4): budgets.per_tenant_hour is an hourly
// ceiling, but counters accumulated forever — one storm permanently exhausted
// the tenant's 5000 cpu-minutes and the scheduler starved (I-10 deny on every
// tick). The hour stamp lets snapshot and reserve lazily zero counters whose
// hour bucket has rolled over. Skips without TEST_PG_DSN.
func TestBudgetWindowResetsOnHourRollover(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping budget window test")
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	const tenant = "org_budgetwindow"

	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM ctrl.budget_counters WHERE tenant_id=$1`, tenant)
	})
	if _, err := st.Pool.Exec(ctx, `DELETE FROM ctrl.budget_counters WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}

	limits := BudgetLimits{BudgetCPUMinutes: 100}
	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		return ReserveBudgetsTx(ctx, tx, tenant, 1, BudgetDeltas{BudgetCPUMinutes: 60}, limits)
	})
	if err != nil {
		t.Fatalf("reserve in current hour: %v", err)
	}

	// Simulate that this usage belongs to LAST hour.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE ctrl.budget_counters SET window_started_at = now() - interval '2 hours' WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatalf("age window: %v", err)
	}

	snap, err := st.BudgetUsageSnapshot(ctx, []string{tenant})
	if err != nil {
		t.Fatalf("snapshot after rollover: %v", err)
	}
	if got := snap[tenant][BudgetCPUMinutes]; got != 0 {
		t.Fatalf("usage from a previous hour must read as zero, got %d", got)
	}

	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		return ReserveBudgetsTx(ctx, tx, tenant, 2, BudgetDeltas{BudgetCPUMinutes: 60}, limits)
	})
	if err != nil {
		t.Fatalf("reserve in the fresh hour must fit the ceiling: %v", err)
	}
	snap, err = st.BudgetUsageSnapshot(ctx, []string{tenant})
	if err != nil {
		t.Fatalf("snapshot after fresh-hour reserve: %v", err)
	}
	if got := snap[tenant][BudgetCPUMinutes]; got != 60 {
		t.Fatalf("fresh hour must start from zero, used=%d want=60", got)
	}
}
