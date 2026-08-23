package planner

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func triggerCase(mutate func(*CandidateInput)) CandidateInput {
	in := baseInput()
	mutate(&in)
	return in
}

func TestPlanFallbackTrigger1Uncertainty(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { low := 0.97; c.SelectionConfidence = &low })
	assertWidened(t, in, FallbackUncertainty)
}

func TestPlanFallbackTrigger2SparseHistory(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.SurfaceSamples = map[string]int{"services": 19} })
	assertWidened(t, in, FallbackSparseHistory)
}

func TestPlanFallbackTrigger3ProtectedPaths(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.ChangedPaths = append(c.ChangedPaths, "auth/oauth/login.go") })
	assertWidened(t, in, FallbackProtectedPaths)
}

func TestPlanFallbackTrigger4FlakeSignal(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.QuarantinedOrFlakeSignals = []string{"TestTotals"} })
	assertWidened(t, in, FallbackFlakeSignal)
}

func TestPlanFallbackTrigger5ConflictOrComposition(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.RelationConflicting = true })
	assertWidened(t, in, FallbackConflictCompose)
}

func TestPlanFallbackTrigger6AmbiguousRetry(t *testing.T) {
	conf := 0.79
	in := triggerCase(func(c *CandidateInput) { c.AmbiguousRetryFailureConfidence = &conf })
	assertWidened(t, in, FallbackAmbiguousRetry)

	atThresh := AmbiguousFailureConfidenceThreshold
	notFired := triggerCase(func(c *CandidateInput) { c.AmbiguousRetryFailureConfidence = &atThresh })
	p, err := Plan(notFired, defaultPolicy())
	require.NoError(t, err)
	require.Empty(t, p.FallbackTriggers, "confidence exactly at threshold is not ambiguous")
}

func TestPlanFallbackTrigger7ExplicitOverride(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.ExplicitFullSuiteOverride = true })
	assertWidened(t, in, FallbackExplicitOverride)

	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	_, okT3 := tierByName(p, 3)
	_, okT4 := tierByName(p, 4)
	require.True(t, okT3, "override plans tier 3")
	require.True(t, okT4, "override plans tier 4")
}

func assertWidened(t *testing.T, in CandidateInput, trigger string) {
	t.Helper()
	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	require.Contains(t, p.FallbackTriggers, trigger)

	tp1, ok := tierByName(p, 1)
	require.True(t, ok)
	require.Contains(t, tp1.Rationale, "fallback:"+trigger)
	require.True(t, hasJob(tp1, "full_unit_suite"), "tier 1 must widen to full suite")
	require.False(t, hasJob(tp1, "selected_unit_tests"))

	tp2, ok := tierByName(p, 2)
	require.True(t, ok)
	require.Contains(t, tp2.Rationale, "fallback:"+trigger)
	require.True(t, hasJob(tp2, "full_integration_suite"))
	require.False(t, hasJob(tp2, "impacted_integration_tests"))
}

func TestPlanMultipleTriggersJoinInCanonicalOrder(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) {
		low := 0.9
		c.SelectionConfidence = &low
		c.SurfaceSamples = nil
	})
	p, err := Plan(in, defaultPolicy())
	require.NoError(t, err)
	require.Equal(t, []string{FallbackUncertainty, FallbackSparseHistory}, p.FallbackTriggers)
	tp1, _ := tierByName(p, 1)
	require.Equal(t, "fallback:"+FallbackUncertainty+",fallback:"+FallbackSparseHistory, tp1.Rationale)
}

func TestPlanNilSelectionConfidenceDefaultsToMaxUncertainty(t *testing.T) {
	in := triggerCase(func(c *CandidateInput) { c.SelectionConfidence = nil })
	assertWidened(t, in, FallbackUncertainty)
}
