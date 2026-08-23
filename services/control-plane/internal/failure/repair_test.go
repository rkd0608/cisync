package failure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func repairCtx() RepairContext {
	return RepairContext{
		IntentOwnedSurfaces: []string{"services/checkout/**", "libs/idempotency/**"},
		ProhibitedPaths:     []string{"infrastructure/prod/**"},
		RiskClass:           "high",
		FailedAssertion:     "totals mismatch",
	}
}

func TestAuthorizeRepairGates(t *testing.T) {
	regression := FailureCase{Classification: ClassFunctionalRegression}

	denied := AuthorizeRepair(regression, testPolicy(2, nil), repairCtx())
	require.False(t, denied.Authorized)
	require.Equal(t, DenyAutonomy, denied.DenyReason)

	denied = AuthorizeRepair(regression, testPolicy(3, []string{"compile_regression"}), repairCtx())
	require.False(t, denied.Authorized)
	require.Equal(t, DenyClassNotRepairable, denied.DenyReason)

	noSurface := repairCtx()
	noSurface.IntentOwnedSurfaces = []string{"infrastructure/prod/**"}
	denied = AuthorizeRepair(regression, testPolicy(3, nil), noSurface)
	require.False(t, denied.Authorized)
	require.Equal(t, DenyNoAllowedSurface, denied.DenyReason)

	badRisk := repairCtx()
	badRisk.RiskClass = "unknown"
	denied = AuthorizeRepair(regression, testPolicy(3, nil), badRisk)
	require.False(t, denied.Authorized)
	require.Equal(t, DenyUnknownRiskClass, denied.DenyReason)

	ok := AuthorizeRepair(regression, testPolicy(3, nil), repairCtx())
	require.True(t, ok.Authorized)
	require.Empty(t, ok.DenyReason)
	require.Equal(t, int64(MaxRepairIterations), ok.Envelope.MaxIterations)
	require.Equal(t, []string{"infrastructure/prod/**"}, ok.Envelope.ProhibitedPaths)
	require.ElementsMatch(t, repairCtx().IntentOwnedSurfaces, ok.Envelope.AllowedPaths)
	require.Contains(t, ok.Envelope.RequiredEvidenceAfterRepair, "payment_contract")
}

func TestAuthorizeRepairSecurityNeverRepaired(t *testing.T) {
	fc := FailureCase{Classification: ClassSecurityPolicyViolation}
	for _, level := range []int{0, 3, 6} {
		got := AuthorizeRepair(fc, testPolicy(level, []string{
			ClassCompileRegression, ClassMergeConflict, ClassFunctionalRegression,
			ClassSecurityPolicyViolation, // hostile policy cannot opt security back in
		}), repairCtx())
		require.False(t, got.Authorized)
		require.Equal(t, DenySecurityViolation, got.DenyReason)
	}
}

func TestAuthorizeRepairIterationClamp(t *testing.T) {
	regression := FailureCase{Classification: ClassCompileRegression}

	rp := testPolicy(3, nil)
	rp.Body.Budgets.PerCandidate.RepairAttempts = 9 // hostile grant must clamp
	got := AuthorizeRepair(regression, rp, repairCtx())
	require.True(t, got.Authorized)
	require.Equal(t, int64(MaxRepairIterations), got.Envelope.MaxIterations)

	rp.Body.Budgets.PerCandidate.RepairAttempts = 1
	got = AuthorizeRepair(regression, rp, repairCtx())
	require.Equal(t, int64(1), got.Envelope.MaxIterations, "stricter policy grant wins")

	rp.Body.Budgets.PerCandidate.RepairAttempts = 0
	got = AuthorizeRepair(regression, rp, repairCtx())
	require.Equal(t, int64(MaxRepairIterations), got.Envelope.MaxIterations, "unset ⇒ default bound")
}
