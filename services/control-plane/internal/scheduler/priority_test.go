package scheduler

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func tel() Telemetry {
	return Telemetry{
		DecisionRateKnown: true, DecisionChangeRate: 0.5,
		RiskClass:    "medium",
		PoolCapacity: -1, QueueDepth: 0, // contention neutral
		BusinessValue: 1.0,
	}
}

func TestPriorityFrozenFormula(t *testing.T) {
	cases := []struct {
		name string
		run  Run
		tel  func(*Telemetry)
		want float64
	}{
		{
			name: "neutral baseline: all sparse defaults, no deadline",
			run:  Run{Tier: 1, EstCostMillicents: CostTier1ForTest},
			tel:  func(x *Telemetry) { *x = Telemetry{RiskClass: "low", PoolCapacity: -1} },
			// 0.5 * 0.4 * 0.5 * 1.0 / (20000 * 1.0)
			want: 0.5 * RiskReductionLow * 0.5 * DefaultBusinessValue / float64(CostTier1ForTest),
		},
		{
			name: "deadline proximity clamps at 72h",
			run:  Run{Tier: 1, EstCostMillicents: CostTier1ForTest},
			tel: func(x *Telemetry) {
				x.RiskClass = "high"
				x.HasDeadline = true
				x.HoursToDeadline = -5 // already past ⇒ proximity 1
			},
			want: 0.5 * RiskReductionHigh * (0.5 + 0.5*1) / float64(CostTier1ForTest),
		},
		{
			name: "prerequisite boost multiplies urgency",
			run:  Run{Tier: 1, EstCostMillicents: CostTier1ForTest},
			tel: func(x *Telemetry) {
				x.RiskClass = "high"
				x.IsPrerequisite = true
			},
			want: 0.5 * RiskReductionHigh * (0.5*PrerequisiteBoost + 0) / float64(CostTier1ForTest),
		},
		{
			name: "staleness term adds linearly",
			run:  Run{Tier: 1, EstCostMillicents: CostTier1ForTest},
			tel: func(x *Telemetry) {
				x.RiskClass = "high"
				x.BaseStalenessHours = 100
			},
			want: 0.5 * RiskReductionHigh * (0.5 + 100*StalenessWeightPerHour) / float64(CostTier1ForTest),
		},
		{
			name: "blast radius caps at one",
			run:  Run{Tier: 1, EstCostMillicents: CostTier1ForTest},
			tel: func(x *Telemetry) {
				x.RiskClass = "critical"
				x.DownstreamDependents = 999
				x.PoolCapacity = 1
				x.QueueDepth = 3 // contention (3+1)/(1+1)=2
			},
			want: 0.5 * RiskReductionCritical * 0.5 / (float64(CostTier1ForTest) * ((3 + 1.0) / (1 + 1.0))),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			telemetry := tel()
			tc.tel(&telemetry)
			got := Priority(tc.run, telemetry)
			require.InDelta(t, tc.want, got, math.Abs(tc.want)*1e-9+1e-12)
		})
	}
}

func TestPriorityGuardsAndDefaults(t *testing.T) {
	// Unknown decision rate → 0.5.
	got := Priority(Run{EstCostMillicents: 100}, tel())
	require.InDelta(t, 0.5*0.7*0.5/100.0, got, 1e-12)

	// Zero cost → planner tier default for tier 4.
	got = Priority(Run{Tier: 4}, tel())
	require.InDelta(t, 0.5*0.7*0.5/4000000.0, got, 1e-15)

	// Missing business value → 1.0; negative decision rate clamps to 0.
	tt := tel()
	tt.BusinessValue = 0
	tt.DecisionChangeRate = -2
	got = Priority(Run{EstCostMillicents: 10}, tt)
	require.InDelta(t, 0.0, got, 1e-12)
}

func TestAgingFloorSlopeAndCap(t *testing.T) {
	require.InDelta(t, 0.01, AgingFloor(1), 1e-12)
	require.InDelta(t, 0.25, AgingFloor(25), 1e-12)
	require.InDelta(t, AgingFloorCap, AgingFloor(50), 1e-12)
	require.InDelta(t, AgingFloorCap, AgingFloor(100000), 1e-12)
	require.InDelta(t, 0.0, AgingFloor(-5), 1e-12)
}

func TestEffectivePriorityNeverBelowFloor(t *testing.T) {
	base := 0.001
	ep := EffectivePriority(base, 60)
	require.InDelta(t, 0.5, ep, 1e-12, "aged run rises to the floor cap")
	ep = EffectivePriority(base, 10)
	require.InDelta(t, 0.1, ep, 1e-12)
	ep = EffectivePriority(0.9, 10)
	require.InDelta(t, 0.9, ep, 1e-12, "strong base priority unaffected by floor")
}

func TestSortRankedTieBreakOrder(t *testing.T) {
	runs := []RankedRun{
		{Run: Run{ID: "r3", CreatedSeq: 30, CreatedULID: "run_03"}, EffectivePriority: 1.0},
		{Run: Run{ID: "r1", CreatedSeq: 10, CreatedULID: "run_02"}, EffectivePriority: 1.0},
		{Run: Run{ID: "r2", CreatedSeq: 20, CreatedULID: "run_01"}, EffectivePriority: 2.0},
		{Run: Run{ID: "r4", CreatedSeq: 10, CreatedULID: "run_01"}, EffectivePriority: 1.0},
	}
	SortRanked(runs)
	got := []string{runs[0].Run.ID, runs[1].Run.ID, runs[2].Run.ID, runs[3].Run.ID}
	require.Equal(t, []string{"r2", "r4", "r1", "r3"}, got,
		"priority desc; tie → older seq first; tie → smaller ULID first")
}

// CostTier1ForTest mirrors the §3 tier-1 default ($0.20).
const CostTier1ForTest = int64(20000)
