package planner

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
	"cisync.dev/cisync/control-plane/internal/policy"
)

func defaultPolicy() policy.ResolvedPolicy {
	rec := policy.DefaultPolicyPack()
	return policy.ResolvedPolicy{PolicyID: rec.ID, Version: rec.Version, Body: rec.Body}
}

func baseInput() CandidateInput {
	conf := 0.995
	return CandidateInput{
		CandidateID:         "cand_01JTEST",
		IntentID:            "int_01JTEST",
		TenantID:            "org_01JT",
		Repo:                "acme/payments",
		BaseSHA:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadSHA:             "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RiskClass:           "medium",
		ChangedPaths:        []string{"services/cart/cart.go", "services/cart/totals.go"},
		Lockfiles:           []string{"go.sum", "package-lock.json"},
		Flags:               []string{"race"},
		Toolchain:           "go1.23",
		SelectionConfidence: &conf,
		SurfaceSamples:      map[string]int{"services": 100},
	}
}

func tierByName(p ValidationPlan, tier int) (TierPlan, bool) {
	for _, tp := range p.Tiers {
		if tp.Tier == tier {
			return tp, true
		}
	}
	return TierPlan{}, false
}

func hasJob(tp TierPlan, name string) bool {
	for _, j := range tp.Jobs {
		if j == name {
			return true
		}
	}
	return false
}

func TestPlanBaseLadder(t *testing.T) {
	p, err := Plan(baseInput(), defaultPolicy())
	require.NoError(t, err)

	for _, want := range []int{0, 1, 2} {
		tp, ok := tierByName(p, want)
		require.True(t, ok, "tier %d must always be planned", want)
		require.NotEmpty(t, tp.Jobs)
		require.NotEmpty(t, tp.Rationale)
	}
	require.Len(t, p.Tiers, 3, "medium risk without triggers stops at tier 2")
	require.Equal(t, "admission_gate", p.Tiers[0].Rationale)
	require.Equal(t, "impact_selection", p.Tiers[1].Rationale)

	tp1, _ := tierByName(p, 1)
	require.True(t, hasJob(tp1, "selected_unit_tests"))
	require.False(t, hasJob(tp1, "full_unit_suite"))
	require.NotNil(t, tp1.SelectionConfidence)
	require.InDelta(t, 0.995, *tp1.SelectionConfidence, 1e-9)

	tp2, _ := tierByName(p, 2)
	require.True(t, hasJob(tp2, "impacted_integration_tests"))
	require.False(t, hasJob(tp2, "payment_contract_check"), "payment job only for high risk")

	require.Empty(t, p.FallbackTriggers)
	require.Equal(t, "pol_payments_high_risk", p.PolicyRef.PolicyID)
	require.Equal(t, 4, p.PolicyRef.Version)
	require.NotEmpty(t, p.InputsHash)
}

func TestPlanTier3ByRiskClass(t *testing.T) {
	in := baseInput()
	in.RiskClass = "high"
	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	_, ok := tierByName(p, 3)
	require.True(t, ok, "high risk ∈ tier3_risk_classes must plan tier 3")

	in.RiskClass = "low"
	p, err = Plan(in, defaultPolicy())
	require.NoError(t, err)
	_, ok = tierByName(p, 3)
	require.False(t, ok)

	high := defaultPolicy()
	high.Body.LadderOverrides.Tier3RiskClasses = []string{"critical"}
	in = baseInput()
	in.RiskClass = "high"
	p, err = Plan(in, high)
	require.NoError(t, err)
	_, ok = tierByName(p, 3)
	require.False(t, ok, "risk outside policy tier3 list must not plan tier 3")
}

func TestPlanTier4CompositionAndOverride(t *testing.T) {
	in := baseInput()
	in.ComposingIntegrationSet = true
	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	tp4, ok := tierByName(p, 4)
	require.True(t, ok)
	require.Contains(t, tp4.Rationale, "composition_validation")
	require.True(t, hasJob(tp4, "merge_train_simulation"))
}

func TestPlanRequiredEvidenceKindsFollowRisk(t *testing.T) {
	for risk, want := range map[string][]string{
		"low":      {"hermetic_build", "selected_unit"},
		"high":     {"hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"},
		"critical": {"hermetic_build", "api_compat", "full_integration", "human_approval"},
	} {
		in := baseInput()
		in.RiskClass = risk
		p, err := Plan(in, defaultPolicy())
		require.NoError(t, err)
		require.Equal(t, want, p.RequiredEvidenceKinds, "risk %s", risk)
	}
}

func TestPlanConditionalContractJobs(t *testing.T) {
	in := baseInput()
	in.RiskClass = "high"
	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	tp2, _ := tierByName(p, 2)
	require.True(t, hasJob(tp2, "payment_contract_check"))
	require.True(t, hasJob(tp2, "idempotency_race_probe"))
}

func TestPlanInvalidInputsFailClosed(t *testing.T) {
	rp := defaultPolicy()
	in := baseInput()
	in.CandidateID = ""
	_, err := Plan(in, rp)
	require.ErrorIs(t, err, ErrInvalidInput)

	in = baseInput()
	in.HeadSHA = in.BaseSHA
	_, err = Plan(in, rp)
	require.ErrorIs(t, err, ErrInvalidInput)

	in = baseInput()
	in.RiskClass = "unknown"
	_, err = Plan(in, rp)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestPlanUnresolvablePolicyStampRejected(t *testing.T) {
	rp := policy.ResolvedPolicy{} // no id/version: integrator bug, fail closed
	_, err := Plan(baseInput(), rp)
	require.ErrorIs(t, err, ErrInvalidInput)
}

// --- property tests ---

func propInput(t *rapidT) CandidateInput {
	conf := rapid.Float64Range(0, 1).Draw(t, "conf")
	samples := int64(rapid.IntRange(0, 40).Draw(t, "samples"))
	in := CandidateInput{
		CandidateID:         "cand_prop",
		IntentID:            "int_prop",
		Repo:                "acme/payments",
		BaseSHA:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadSHA:             "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RiskClass:           rapid.SampledFrom([]string{"low", "medium", "high", "critical"}).Draw(t, "risk"),
		ChangedPaths:        rapid.SliceOfN(rapid.SampledFrom([]string{"services/x.go", "auth/y.go", "payments/z.go", "docs/d.md"}), 0, 4).Draw(t, "paths"),
		Lockfiles:           rapid.SliceOfN(rapid.SampledFrom([]string{"go.sum", "pnpm-lock.yaml"}), 0, 2).Draw(t, "lockfiles"),
		Flags:               rapid.SliceOfN(rapid.SampledFrom([]string{"race", "v"}), 0, 2).Draw(t, "flags"),
		Toolchain:           rapid.SampledFrom([]string{"go1.23", "node20"}).Draw(t, "toolchain"),
		SelectionConfidence: &conf,
		SurfaceSamples:      map[string]int{"services": int(samples)},
	}
	if rapid.Bool().Draw(t, "compose") {
		in.ComposingIntegrationSet = true
	}
	if rapid.Bool().Draw(t, "override") {
		in.ExplicitFullSuiteOverride = true
	}
	if n := int(rapid.IntRange(0, 3).Draw(t, "flake_n")); n > 0 {
		in.QuarantinedOrFlakeSignals = make([]string, n)
	}
	if rapid.Bool().Draw(t, "ambig") {
		ac := rapid.Float64Range(0, 1).Draw(t, "ambig_conf")
		in.AmbiguousRetryFailureConfidence = &ac
	}
	return in
}

type rapidT = rapid.T

func positionOf(tiers []TierPlan, tier int) int {
	for i, tp := range tiers {
		if tp.Tier == tier {
			return i
		}
	}
	return -1
}
