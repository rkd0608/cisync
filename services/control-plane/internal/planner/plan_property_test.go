package planner

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestHashInputsSensitivityAndOrderInsensitivity(t *testing.T) {
	base := InputsMaterial{
		BaseSHA:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Lockfiles: []string{"go.sum", "go.mod"},
		Flags:     []string{"race", "-count=1"},
		Toolchain: "go1.23.3",
	}
	h := HashInputs(base)

	reordered := InputsMaterial{BaseSHA: base.BaseSHA, Lockfiles: []string{"go.mod", "go.sum"}, Flags: []string{"-count=1", "race"}, Toolchain: base.Toolchain}
	require.Equal(t, h, HashInputs(reordered), "slice order must not affect the reuse key")

	for i, mutate := range []func(*InputsMaterial){
		func(m *InputsMaterial) { m.BaseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
		func(m *InputsMaterial) { m.Lockfiles = append(m.Lockfiles, "Cargo.lock") },
		func(m *InputsMaterial) { m.Flags = append(m.Flags, "v") },
		func(m *InputsMaterial) { m.Toolchain = "go1.22" },
	} {
		m := base
		mutate(&m)
		require.NotEqual(t, h, HashInputs(m), "mutation %d must change inputs_hash", i)
	}
}

func TestSurfaceClassesDerivation(t *testing.T) {
	require.Equal(t, []string{"_root", "services"}, SurfaceClasses([]string{"README.md", "services/a/b.go", "services/c.go"}))
}

func TestPropertyPlanDeterministic(t *testing.T) {
	rp := defaultPolicy()
	rapid.Check(t, func(t *rapid.T) {
		in := propInput(t)
		a, errA := Plan(in, rp)
		b, errB := Plan(in, rp)
		require.Equal(t, errA == nil, errB == nil)
		if errA != nil {
			return
		}
		require.True(t, reflect.DeepEqual(a, b), "identical inputs must yield identical plans")
	})
}

func TestPropertyPlanLadderShape(t *testing.T) {
	rp := defaultPolicy()
	rapid.Check(t, func(t *rapid.T) {
		in := propInput(t)
		p, err := Plan(in, rp)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(p.Tiers), 3)
		prev := -1
		for i, tp := range p.Tiers {
			require.Equal(t, i, positionOf(p.Tiers, tp.Tier), "tiers strictly ascending")
			require.Greater(t, tp.Tier, prev, "tier numbers ascend")
			require.NotEmpty(t, tp.Jobs)
			require.NotEmpty(t, tp.Rationale)
			prev = tp.Tier
		}
		require.Equal(t, 0, p.Tiers[0].Tier)
		tp1, _ := tierByName(p, 1)
		require.NotNil(t, tp1.SelectionConfidence, "selection_confidence present on deferring tiers")
		tp2, _ := tierByName(p, 2)
		require.NotNil(t, tp2.SelectionConfidence)
	})
}

func TestPropertyPlanFallbackImpliesWidening(t *testing.T) {
	rp := defaultPolicy()
	rapid.Check(t, func(t *rapid.T) {
		in := propInput(t)
		p, err := Plan(in, rp)
		require.NoError(t, err)
		widened := len(p.FallbackTriggers) > 0
		tp1, _ := tierByName(p, 1)
		if widened {
			require.Contains(t, tp1.Rationale, "fallback:")
			require.True(t, hasJob(tp1, "full_unit_suite"))
		} else {
			require.NotContains(t, tp1.Rationale, "fallback:")
			require.False(t, hasJob(tp1, "full_unit_suite"))
			require.True(t, hasJob(tp1, "selected_unit_tests"))
		}
	})
}

func TestPropertyPlanHighRiskAlwaysIncludesTier3(t *testing.T) {
	rp := defaultPolicy() // tier3_risk_classes = [high critical]
	rapid.Check(t, func(t *rapid.T) {
		in := propInput(t)
		in.RiskClass = rapid.SampledFrom([]string{"high", "critical"}).Draw(t, "risk")
		p, err := Plan(in, rp)
		require.NoError(t, err)
		_, ok := tierByName(p, 3)
		require.True(t, ok, "high/critical risk must plan tier 3 under default policy")
	})
}
