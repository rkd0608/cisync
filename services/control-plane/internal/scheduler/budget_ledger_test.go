package scheduler

import (
	"testing"

	"sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/store"
)

// P0-3 regression: a tenant with NO budget_counters row has consumed nothing,
// so its remaining budget must equal the full policy ceiling. The snapshot
// only returns tenants that already hold rows; deriving the ledger from the
// snapshot alone silently funded nobody and starved every fresh tenant's
// dispatch (live W4 finding: 340 runs stuck queued, zero admissions).
func TestRemainingBudgetsFundsTenantsWithoutCounterRows(t *testing.T) {
	ceilings := policy.PerTenantHourBudget{CPUMinutes: 5000, ConcurrentCandidates: 40}

	got := remainingBudgets(ceilings, nil, []string{"tenant_fresh"})
	if got.TenantCPURemaining["tenant_fresh"] != 5000 {
		t.Fatalf("fresh tenant cpu remaining = %d, want full ceiling 5000", got.TenantCPURemaining["tenant_fresh"])
	}
	if got.TenantConcurrentRemaining["tenant_fresh"] != 40 {
		t.Fatalf("fresh tenant concurrency remaining = %d, want full ceiling 40", got.TenantConcurrentRemaining["tenant_fresh"])
	}
}

// Partial rows: a tenant that consumed one kind must still get the full
// ceiling on kinds it never touched.
func TestRemainingBudgetsDefaultsUntouchedKindsToCeiling(t *testing.T) {
	ceilings := policy.PerTenantHourBudget{CPUMinutes: 100, ConcurrentCandidates: 10}
	usage := map[string]map[store.BudgetKind]int64{
		"tenant_partial": {store.BudgetCPUMinutes: 30},
	}

	got := remainingBudgets(ceilings, usage, []string{"tenant_partial"})
	if got.TenantCPURemaining["tenant_partial"] != 70 {
		t.Fatalf("cpu remaining = %d, want 70", got.TenantCPURemaining["tenant_partial"])
	}
	if got.TenantConcurrentRemaining["tenant_partial"] != 10 {
		t.Fatalf("concurrency remaining = %d, want untouched ceiling 10", got.TenantConcurrentRemaining["tenant_partial"])
	}
}
