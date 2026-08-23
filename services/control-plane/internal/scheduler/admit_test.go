package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func caps(tiers map[int]int) Caps { return Caps{WIPByTier: tiers} }

func budgets(cpu, conc map[string]int64) BudgetLedger {
	return BudgetLedger{TenantCPURemaining: cpu, TenantConcurrentRemaining: conc}
}

func ranked(id, tenant, cand string, tier int, ep float64, durMS int64) RankedRun {
	return RankedRun{
		Run: Run{
			ID: id, CandidateID: cand, TenantID: tenant, Tier: tier,
			Pool: "unit", EstDurationMS: durMS,
			CreatedSeq: 1, CreatedULID: "run_" + id,
		},
		EffectivePriority: ep,
	}
}

func TestAdmitAllWithinBudget(t *testing.T) {
	batch := []RankedRun{
		ranked("r1", "org_a", "c1", 1, 2.0, 120000), // 2 cpu-min
		ranked("r2", "org_a", "c1", 1, 1.0, 60000),  // same candidate ⇒ shared slot
	}
	res := Admit(batch, caps(map[int]int{1: 5}), WIPSnapshot{InFlightByTier: map[int]int{1: 0}},
		budgets(map[string]int64{"org_a": 100}, map[string]int64{"org_a": 10}))

	require.Len(t, res.Admissions, 2)
	for _, a := range res.Admissions {
		require.True(t, a.Admitted, "%s must admit", a.RunID)
	}
	require.Equal(t, 2, res.AdmittedCount)
	require.Equal(t, int64(3), res.Deltas.CPUMinutesByTenant["org_a"])
	require.Equal(t, int64(1), res.Deltas.ConcurrentByTenant["org_a"], "same candidate consumes one slot")
	require.Equal(t, 2, res.Deltas.WIPAddedByTier[1])
}

func TestAdmitDenialReasonsAndZeroDeltaForDenied(t *testing.T) {
	capOnly := caps(map[int]int{1: 1})
	snap := WIPSnapshot{InFlightByTier: map[int]int{1: 1}}
	fat := budgets(map[string]int64{"org_a": 100}, map[string]int64{"org_a": 10})
	broke := budgets(map[string]int64{"org_a": 0}, map[string]int64{"org_a": 10})
	noConc := budgets(map[string]int64{"org_a": 100}, map[string]int64{"org_a": 0})

	res := Admit([]RankedRun{ranked("r1", "org_a", "c1", 1, 1, 60000)}, capOnly, snap, fat)
	require.False(t, res.Admissions[0].Admitted)
	require.Equal(t, DenyWIPCap, res.Admissions[0].DenyReason)

	res = Admit([]RankedRun{ranked("r1", "org_a", "c1", 1, 1, 60000)}, capOnly, WIPSnapshot{InFlightByTier: map[int]int{}}, broke)
	require.Equal(t, DenyTenantCPU, res.Admissions[0].DenyReason)

	res = Admit([]RankedRun{ranked("r1", "org_a", "c1", 1, 1, 60000)}, capOnly, WIPSnapshot{}, noConc)
	require.Equal(t, DenyTenantConc, res.Admissions[0].DenyReason)

	for _, d := range []Deltas{res.Deltas} {
		require.Empty(t, d.CPUMinutesByTenant)
		require.Empty(t, d.ConcurrentByTenant)
		require.Empty(t, d.WIPAddedByTier)
	}
}

func TestAdmitUnconfiguredTierFailsClosed(t *testing.T) {
	res := Admit([]RankedRun{ranked("r1", "o", "c", 3, 9, 60000)}, caps(map[int]int{1: 10}),
		WIPSnapshot{InFlightByTier: map[int]int{3: 0}},
		budgets(map[string]int64{"o": 100}, map[string]int64{"o": 10}))
	require.False(t, res.Admissions[0].Admitted)
	require.Equal(t, DenyWIPCap, res.Admissions[0].DenyReason, "missing tier cap denies (fail-closed)")
}

func TestAdmitProcessesInPriorityOrderUnderScarcity(t *testing.T) {
	batch := []RankedRun{
		ranked("low", "o", "clow", 1, 0.1, 60000),
		ranked("high", "o", "chigh", 1, 9.0, 60000),
	}
	res := Admit(batch, caps(map[int]int{1: 1}), WIPSnapshot{InFlightByTier: map[int]int{}},
		budgets(map[string]int64{"o": 1}, map[string]int64{"o": 10}))
	require.True(t, res.Admissions[0].Admitted)
	require.Equal(t, "high", res.Admissions[0].RunID, "scarce slot goes to the higher priority run")
	require.False(t, res.Admissions[1].Admitted)
}

func TestAdmitConservationManualCheck(t *testing.T) {
	batch := []RankedRun{
		ranked("r1", "o1", "c1", 2, 3.0, 90000), // ceil(90/60)=2 min
		ranked("r2", "o1", "c2", 2, 2.0, 30000), // 1 min
		ranked("r3", "o2", "c3", 2, 1.0, 61000), // 2 min
	}
	cpuBefore := map[string]int64{"o1": 3, "o2": 1}
	concBefore := map[string]int64{"o1": 2, "o2": 1}
	wipBefore := map[int]int{2: 1}
	res := Admit(batch, caps(map[int]int{2: 4}), WIPSnapshot{InFlightByTier: wipBefore},
		budgets(cpuBefore, concBefore))

	// o1 has cpu 3 → r1(2)+r2(1)=3 fits; conc 2 → c1 and c2 each take one.
	require.True(t, res.Admissions[0].Admitted)
	require.True(t, res.Admissions[1].Admitted)
	require.Equal(t, int64(3), res.Deltas.CPUMinutesByTenant["o1"])
	require.Equal(t, int64(2), res.Deltas.ConcurrentByTenant["o1"])

	// o2 has cpu 1 < need 2 ⇒ denied entirely; zero partial reservation.
	require.False(t, res.Admissions[2].Admitted)
	require.NotContains(t, res.Deltas.CPUMinutesByTenant, "o2")

	require.Equal(t, 2, res.Deltas.WIPAddedByTier[2], "only admitted runs add WIP")
}
