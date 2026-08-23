package scheduler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

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
