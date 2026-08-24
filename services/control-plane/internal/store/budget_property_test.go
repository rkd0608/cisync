package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"pgregory.net/rapid"
)

/**
 * I-06 property: arbitrary admit(reserve)/complete(release)/cancel(release)
 * interleavings must conserve Σreservations − Σreleases == used against the
 * real PG counters. rapid drives random op sequences; the model tracks the
 * outstanding (reserved-not-yet-released) amounts per tenant/kind. Skips
 * without TEST_PG_DSN.
 */

type budgetOp struct {
	kind    BudgetKind
	reserve int64
	release int64
}

func tenantFor(t *testing.T, st *Store) string {
	t.Helper()
	return "org_propbudget" // synthetic; budget_counters is keyed freely
}

// TestBudgetConservationProperty is the crash-safe conservation check: every
// op commits in its own tx exactly like production reserve/release paths.
func TestBudgetConservationProperty(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping budget property test")
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenant := tenantFor(t, st)
	kinds := []BudgetKind{BudgetCPUMinutes, BudgetConcurrentCandidates, BudgetRepairAttempts}

	// P0-3: ReserveBudgetsTx fails closed when a kind has no configured
	// limit, so this conservation harness pins an effectively-unbounded one;
	// the deny path is covered separately by TestReserveEnforcesLimitAtomically.
	noCeiling := BudgetLimits{}
	for _, k := range kinds {
		noCeiling[k] = 1 << 40
	}

	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()
		// Zero the baseline INSIDE each rapid iteration: counters persist
		// across iterations (and suite runs), and conservation is asserted
		// against a zero start.
		if _, err := st.Pool.Exec(ctx,
			`DELETE FROM ctrl.budget_counters WHERE tenant_id=$1`, tenant); err != nil {
			t.Fatalf("reset counters: %v", err)
		}
		outstanding := map[BudgetKind]int64{}
		for _, k := range kinds {
			outstanding[k] = 0
		}

		ops := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) budgetOp {
			kind := rapid.SampledFrom(kinds).Draw(t, "kind")
			op := budgetOp{kind: kind}
			if outstanding[kind] > 0 && rapid.Bool().Draw(t, "releaseFirst") {
				// Releases never exceed what is outstanding — mirroring the
				// production rule that only reserved work can be released.
				op.release = rapid.Int64Range(1, outstanding[kind]).Draw(t, "releaseAmt")
			} else {
				op.reserve = rapid.Int64Range(1, 20).Draw(t, "reserveAmt")
			}
			return op
		}), 1, 40).Draw(t, "ops")

		for i, op := range ops {
			err := st.ExecTx(ctx, func(tx pgx.Tx) error {
				if op.reserve > 0 {
					return ReserveBudgetsTx(ctx, tx, tenant, int64(i), BudgetDeltas{op.kind: op.reserve}, noCeiling)
				}
				return ReleaseBudgetsTx(ctx, tx, tenant, int64(i), BudgetDeltas{op.kind: op.release})
			})
			if err != nil {
				t.Fatalf("op %d failed: %v", i, err)
			}
			if op.reserve > 0 {
				outstanding[op.kind] += op.reserve
			} else {
				outstanding[op.kind] -= op.release
			}
		}

		snapshot, err := st.BudgetUsageSnapshot(ctx, []string{tenant})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for kind, expected := range outstanding {
			if snapshot[tenant][kind] != expected {
				t.Fatalf("conservation violated for %s: used=%d want=%d",
					kind, snapshot[tenant][kind], expected)
			}
		}
	})
}

// TestReserveEnforcesLimitAtomically pins the deny path: crossing the policy
// ceiling fails the tx and rolls the counter back to its prior value.
func TestReserveEnforcesLimitAtomically(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping budget limit test")
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
	tenant := tenantFor(t, st)

	// Zero the baseline: the rapid conservation test shares this tenant and
	// leaves arbitrary counter values behind.
	if _, err := st.Pool.Exec(ctx,
		`DELETE FROM ctrl.budget_counters WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatalf("reset counters: %v", err)
	}

	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		return ReserveBudgetsTx(ctx, tx, tenant, 1, BudgetDeltas{BudgetCPUMinutes: 5},
			BudgetLimits{BudgetCPUMinutes: 100})
	})
	if err != nil {
		t.Fatalf("initial reserve: %v", err)
	}

	err = st.ExecTx(ctx, func(tx pgx.Tx) error {
		return ReserveBudgetsTx(ctx, tx, tenant, 2, BudgetDeltas{BudgetCPUMinutes: 96},
			BudgetLimits{BudgetCPUMinutes: 100})
	})
	if err == nil {
		t.Fatal("reservation exceeding the ceiling must be denied")
	}

	var used int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT used FROM ctrl.budget_counters WHERE tenant_id=$1 AND kind='cpu_minutes'`,
		tenant).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 5 {
		t.Fatalf("denied reservation must roll back entirely, got used=%d", used)
	}
}
