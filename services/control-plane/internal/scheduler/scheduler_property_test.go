package scheduler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// --- generators ---

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

// --- properties ---

// TestPropertyAdmissionNeverExceedsCapsOrBudgets is the I-06/I-10 core: for
// arbitrary loads, every admitted run fit ALL its dimensions atomically and
// the deltas exactly equal an independent recount of admitted reservations.
func TestPropertyAdmissionNeverExceedsCapsOrBudgets(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		batch := genBatch(t)
		capsG := genCaps(t)
		wip := genWIP(t)
		budgetsG := genBudgets(t)

		res := Admit(batch, capsG, wip, budgetsG)

		cpuUsed := map[string]int64{}
		concUsed := map[string]int64{}
		wipAdded := map[int]int{}
		admittedCands := map[string]map[string]struct{}{}

		require.Len(t, res.Admissions, len(batch))
		for _, a := range res.Admissions {
			var run Run
			found := false
			for _, rr := range batch {
				if rr.Run.ID == a.RunID {
					run = rr.Run
					found = true
					break
				}
			}
			require.True(t, found)
			if !a.Admitted {
				require.NotEmpty(t, a.DenyReason, "denials carry a machine reason")
				continue
			}
			need := cpuMinutes(run.EstDurationMS)
			concNeed := int64(0)
			if admittedCands[run.TenantID] == nil {
				admittedCands[run.TenantID] = map[string]struct{}{}
			}
			if _, dup := admittedCands[run.TenantID][run.CandidateID]; !dup {
				concNeed = 1
			}

			capTier, capped := capOf(capsG, run.Tier)
			require.True(t, capped, "admitted on unconfigured tier")
			require.LessOrEqual(t, wip.InFlightByTier[run.Tier]+wipAdded[run.Tier]+1, capTier,
				"WIP cap overrun")
			require.GreaterOrEqual(t, budgetsG.TenantCPURemaining[run.TenantID]-cpuUsed[run.TenantID], need,
				"cpu budget overrun")
			require.GreaterOrEqual(t, budgetsG.TenantConcurrentRemaining[run.TenantID]-concUsed[run.TenantID], concNeed,
				"concurrency cap overrun")

			if need > 0 {
				cpuUsed[run.TenantID] += need
			}
			if concNeed > 0 {
				concUsed[run.TenantID] += concNeed
			}
			wipAdded[run.Tier]++
			admittedCands[run.TenantID][run.CandidateID] = struct{}{}
		}

		for tenant, used := range cpuUsed {
			require.Equal(t, used, res.Deltas.CPUMinutesByTenant[tenant], "conservation cpu "+tenant)
		}
		require.Len(t, res.Deltas.CPUMinutesByTenant, len(cpuUsed))
		for tenant, used := range concUsed {
			require.Equal(t, used, res.Deltas.ConcurrentByTenant[tenant], "conservation conc "+tenant)
		}
		require.Len(t, res.Deltas.ConcurrentByTenant, len(concUsed))
		for tier, added := range wipAdded {
			require.Equal(t, added, res.Deltas.WIPAddedByTier[tier])
		}
	})
}

// TestPropertyAdmitDeterministicUnderPermutation: identical inputs in any
// input order produce the identical admission sequence (pure function).
func TestPropertyAdmitDeterministicUnderPermutation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		batch := genBatch(t)
		capsG := genCaps(t)
		wip := genWIP(t)
		budgetsG := genBudgets(t)

		reversed := make([]RankedRun, len(batch))
		for i, rr := range batch {
			reversed[len(batch)-1-i] = rr
		}
		a := Admit(batch, capsG, wip, budgetsG)
		b := Admit(reversed, capsG, wip, budgetsG)
		require.Equal(t, a, b)
	})
}

// TestPropertyPriorityTotalAndDeterministic: the frozen formula never
// produces NaN/Inf/negative output for arbitrary telemetry and repeats
// exactly on identical inputs.
func TestPropertyPriorityTotalAndDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		run := Run{
			Tier:              rapid.IntRange(-1, 5).Draw(t, "tier"),
			EstDurationMS:     int64(rapid.IntRange(-1000, 3600000).Draw(t, "dur")),
			EstCostMillicents: int64(rapid.IntRange(-100, 5000000).Draw(t, "cost")),
		}
		tel := Telemetry{
			DecisionRateKnown:    rapid.Bool().Draw(t, "known"),
			DecisionChangeRate:   rapid.Float64Range(-10, 10).Draw(t, "rate"),
			RiskClass:            rapid.SampledFrom([]string{"low", "medium", "high", "critical", "weird"}).Draw(t, "risk"),
			DownstreamDependents: rapid.IntRange(-5, 200).Draw(t, "dependents"),
			HasDeadline:          rapid.Bool().Draw(t, "has_deadline"),
			HoursToDeadline:      rapid.Float64Range(-100, 10000).Draw(t, "hours"),
			IsPrerequisite:       rapid.Bool().Draw(t, "prereq"),
			BaseStalenessHours:   rapid.Float64Range(-50, 100000).Draw(t, "stale"),
			BusinessValue:        rapid.Float64Range(-5, 5).Draw(t, "bv"),
			QueueDepth:           rapid.IntRange(0, 100000).Draw(t, "depth"),
			PoolCapacity:         rapid.IntRange(-1, 10000).Draw(t, "capacity"),
		}
		a := Priority(run, tel)
		b := Priority(run, tel)
		require.Equal(t, a, b)
		require.False(t, a != a, "NaN priority")         // NaN check
		require.False(t, a > 1e308 || a < -1e308, "Inf") // Inf check
		require.GreaterOrEqual(t, a, 0.0)
	})
}

// TestPropertySortRankedPermutationInvariant pins I-13 determinism: any two
// orderings of the same runs sort to the identical sequence.
func TestPropertySortRankedPermutationInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		rs := make([]RankedRun, 0, n)
		for i := 0; i < n; i++ {
			rs = append(rs, RankedRun{
				Run: Run{
					ID:          fmt.Sprintf("r%03d", i),
					CreatedSeq:  int64(rapid.IntRange(0, 50).Draw(t, "seq")),
					CreatedULID: fmt.Sprintf("01H%026d", i), // real ULIDs are unique
				},
				EffectivePriority: float64(rapid.IntRange(0, 9).Draw(t, "ep")),
			})
		}
		sorted := make([]RankedRun, len(rs))
		copy(sorted, rs)
		SortRanked(sorted)

		reversed := make([]RankedRun, len(rs))
		for i, r := range rs {
			reversed[len(rs)-1-i] = r
		}
		SortRanked(reversed)
		require.Equal(t, sorted, reversed)
	})
}

type simRun struct {
	id    string
	base  float64
	birth int
}

// TestPropertyNoStarvationAgingFloorGuaranteesDispatch (EC-025): under a
// sustained stream of higher-priority arrivals against capacity 1/tick, the
// lowest-priority victim — enqueued at tick 0 — must be admitted within a
// bounded horizon. The bound follows from the formula: once the victim ages
// AgingFloorCap/AgingFloorSlopePerHour hours its floor reaches the cap 0.5,
// which dominates every arrival base (< 0.5); equal-floor ties resolve to
// the oldest ledger sequence, which is always the victim's. So the bound is
// last-arrival-tick + 50h + slack.
func TestPropertyNoStarvationAgingFloorGuaranteesDispatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		streamTicks := rapid.IntRange(5, 60).Draw(t, "stream_ticks")
		maxArrivalsPerTick := rapid.IntRange(0, 2).Draw(t, "arrivals_per_tick")

		const tickHours = 1.0
		capsG := Caps{WIPByTier: map[int]int{1: 1}}
		budgetsG := BudgetLedger{
			TenantCPURemaining:        map[string]int64{"org_a": 1 << 40},
			TenantConcurrentRemaining: map[string]int64{"org_a": 1 << 40},
		}

		queue := []simRun{{id: "victim", base: 0.001, birth: 0}}
		lastArrivalTick := 0
		arrSeq := 0

		for tick := 0; tick <= streamTicks+60; tick++ {
			if tick < streamTicks {
				k := rapid.IntRange(0, maxArrivalsPerTick).Draw(t, fmt.Sprintf("arrivals@%d", tick))
				for j := 0; j < k; j++ {
					base := rapid.Float64Range(0.01, 0.49).Draw(t, "arrival_base")
					queue = append(queue, simRun{id: fmt.Sprintf("arr_%d_%d", tick, arrSeq), base: base, birth: tick})
					arrSeq++
					lastArrivalTick = tick
				}
			}

			rankedQueue := make([]RankedRun, len(queue))
			for i, sr := range queue {
				age := float64(tick-sr.birth) * tickHours
				rankedQueue[i] = ranked(sr.id, "org_a", sr.id, 1, 0, 60000)
				rankedQueue[i].EffectivePriority = EffectivePriority(sr.base, age)
			}

			res := Admit(rankedQueue, capsG, WIPSnapshot{}, budgetsG)

			var remaining []simRun
			victimDone := false
			for i, a := range res.Admissions {
				if a.Admitted {
					if rankedQueue[i].Run.ID == "victim" {
						victimDone = true
					}
					continue
				}
				remaining = append(remaining, queue[i])
			}
			queue = remaining

			if victimDone {
				bound := lastArrivalTick + int(AgingFloorCap/AgingFloorSlopePerHour) + 2
				require.LessOrEqual(t, tick, bound,
					"victim must dispatch by tick %d via the aging floor", bound)
				return
			}
			require.NotEmpty(t, queue, "queue must never lose runs silently")
		}
		t.Fatalf("starvation: victim never dispatched within the proven bound")
	})
}
