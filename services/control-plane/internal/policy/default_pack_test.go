package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultPolicyPackMatchesSpec pins the compiled-in pack to the
// DOMAIN_MODEL_DRAFT §8 document, field by field.
func TestDefaultPolicyPackMatchesSpec(t *testing.T) {
	require.NoError(t, DefaultPolicyPackErr())
	rec := DefaultPolicyPack()
	require.Equal(t, "pol_payments_high_risk", rec.ID)
	require.Equal(t, 4, rec.Version)
	require.Equal(t, StatusActive, rec.Status)

	body := rec.Body

	sel := body.ScopeSelectors
	require.Equal(t, []string{"acme/payments"}, sel.Repos)
	require.Equal(t, []string{"services/checkout/**", "libs/idempotency/**"}, sel.Paths)
	require.Equal(t, []string{"high", "critical"}, sel.RiskClasses)
	require.Equal(t, []string{"agent:*"}, sel.Actors.Agents)
	require.Equal(t, []string{"agent:docs-writer"}, sel.Actors.Exclude)

	require.Equal(t, map[string][]string{
		"low":      {"hermetic_build", "selected_unit"},
		"medium":   {"hermetic_build", "selected_unit", "api_compat"},
		"high":     {"hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"},
		"critical": {"hermetic_build", "api_compat", "full_integration", "human_approval"},
	}, body.RequiredEvidenceByRisk)

	lad := body.LadderOverrides
	require.Equal(t, []string{"high", "critical"}, lad.Tier3RiskClasses)
	require.InDelta(t, 0.98, lad.MinSelectionConfidence, 1e-9)
	require.Equal(t, []string{"uncertainty_gt_0.02", "sparse_history_lt_20", "protected_paths"}, lad.FallbackTriggers)
	require.Equal(t, []string{"auth/**", "migrations/**", "infrastructure/prod/**"}, lad.ProtectedPaths)

	b := body.Budgets
	require.Equal(t, int64(120), b.PerCandidate.CPUMinutes)
	require.Equal(t, int64(30), b.PerCandidate.EnvironmentMinutes)
	require.Equal(t, int64(2), b.PerCandidate.RepairAttempts)
	require.Equal(t, int64(5000), b.PerTenantHour.CPUMinutes)
	require.Equal(t, int64(40), b.PerTenantHour.ConcurrentCandidates)
	require.Equal(t, map[string]int{"0": 200, "1": 60, "2": 20, "3": 6, "4": 2}, b.WIPByTier)
	require.Equal(t, map[string]EnvTemplateCap{"payments-preview": {MaxConcurrent: 4}}, b.EnvTemplates)
	require.InDelta(t, 1.5, b.ValueTiers["acme/payments"], 1e-9)
	require.InDelta(t, 0.3, b.ValueTiers["acme/docs"], 1e-9)

	a := body.Autonomy
	require.Equal(t, 3, a.Level)
	require.Len(t, a.LevelsSemantics, 7)
	require.Empty(t, a.AutoMergeRiskClasses)
	require.Equal(t, []string{"compile_regression", "merge_conflict", "functional_regression"}, a.AutoRepairClasses)
	require.Equal(t, []string{"security_policy_violation", "test_expectation_drift", "classification_confidence_lt_0.8"}, a.EscalateOn)
}

func TestDefaultRegistryServesActiveDefault(t *testing.T) {
	got, err := Resolve(
		Subject{Repo: "acme/payments", ChangedPaths: []string{"services/checkout/x.go"}, RiskClass: "high", Actor: "agent:a1"},
		DefaultRegistry(),
	)
	require.NoError(t, err)
	require.Equal(t, "pol_payments_high_risk", got.PolicyID,
		"the specific payments pack must win when its selectors match")
	require.Equal(t, 4, got.Version)
}

// The wildcard fallback keeps every API-accepted risk class resolvable
// (I-09 fail-closed otherwise blocks low/medium intents outright).
func TestDefaultRegistryWildcardFallback(t *testing.T) {
	for _, risk := range []string{"low", "medium", "high"} {
		got, err := Resolve(Subject{Repo: "other/repo", RiskClass: risk}, DefaultRegistry())
		require.NoError(t, err, "risk %s", risk)
		require.Equal(t, "pol_cisync_default", got.PolicyID)
		require.Equal(t, 1, got.Version)
	}
}
