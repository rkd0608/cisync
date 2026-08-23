package scheduler

import (
	"fmt"

	"pgregory.net/rapid"
)

// Shared rapid generators for admission, priority and ordering properties.

var genTenantID = rapid.SampledFrom([]string{"org_a", "org_b", "org_c"})

func genRun(t *rapid.T, seq int64) Run {
	return Run{
		ID:                fmt.Sprintf("run_%06d", seq),
		CandidateID:       fmt.Sprintf("cand_%02d", rapid.IntRange(0, 6).Draw(t, "cand")),
		TenantID:          genTenantID.Draw(t, "tenant"),
		Pool:              "unit",
		Tier:              rapid.IntRange(0, 4).Draw(t, "tier"),
		JobKind:           "selected_unit",
		EstDurationMS:     int64(rapid.IntRange(0, 720000).Draw(t, "dur")),
		EstCostMillicents: 0, // exercise tier-default costs
		CreatedSeq:        seq,
		CreatedULID:       fmt.Sprintf("01H%022d", seq),
	}
}

func genCaps(t *rapid.T) Caps {
	caps := Caps{WIPByTier: map[int]int{}}
	for tier := 0; tier <= 4; tier++ {
		if rapid.Bool().Draw(t, fmt.Sprintf("cap_tier%d_configured", tier)) {
			caps.WIPByTier[tier] = rapid.IntRange(0, 8).Draw(t, fmt.Sprintf("cap_tier%d", tier))
		} else {
			caps.WIPByTier[tier] = -1 // sentinel: unconfigured ⇒ fail-closed
		}
	}
	return caps
}

func capOf(c Caps, tier int) (int, bool) {
	v, ok := c.WIPByTier[tier]
	if !ok || v < 0 {
		return 0, false
	}
	return v, true
}

func genWIP(t *rapid.T) WIPSnapshot {
	wip := WIPSnapshot{InFlightByTier: map[int]int{}}
	for tier := 0; tier <= 4; tier++ {
		wip.InFlightByTier[tier] = rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("wip_tier%d", tier))
	}
	return wip
}

func genBudgets(t *rapid.T) BudgetLedger {
	b := BudgetLedger{
		TenantCPURemaining:        map[string]int64{},
		TenantConcurrentRemaining: map[string]int64{},
	}
	for _, tenant := range []string{"org_a", "org_b", "org_c"} {
		b.TenantCPURemaining[tenant] = int64(rapid.IntRange(-2, 40).Draw(t, "cpu_"+tenant))
		b.TenantConcurrentRemaining[tenant] = int64(rapid.IntRange(-1, 5).Draw(t, "conc_"+tenant))
	}
	return b
}

func genBatch(t *rapid.T) []RankedRun {
	n := rapid.IntRange(0, 25).Draw(t, "batch_size")
	batch := make([]RankedRun, 0, n)
	for i := 0; i < n; i++ {
		r := genRun(t, int64(i))
		ep := float64(rapid.IntRange(-10, 100).Draw(t, "ep")) / 10.0
		batch = append(batch, RankedRun{Run: r, EffectivePriority: ep})
	}
	return batch
}
