package failure

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
	"sauron.dev/sauron/control-plane/internal/policy"
)

const logCart = `2026-08-23T03:41:00Z INFO  runner worker=7 pid=2214
--- FAIL: TestTotals (0.02s)
    cart_test.go:42: expected 100 got 104
FAIL	sauron.dev/sauron/control-plane/services/cart	0.512s
`

func TestNormalizeLogStableTokens(t *testing.T) {
	n := NormalizeLog(logCart)
	require.NotContains(t, n, "2026-08-23T03:41:00Z")
	require.NotContains(t, n, "0.512s")
	require.Contains(t, n, "--- FAIL: TestTotals")
}

func TestSignatureDigestInvariance(t *testing.T) {
	a := SignatureDigest("expected 42 got 43 at 12:00:01 in 250ms")
	b := SignatureDigest("expected 99 got 100 at 23:59:59 in 880ms")
	require.Equal(t, a, b, "numeric-only differences must not change the signature")
	c := SignatureDigest("assertion failed: cart totals mismatch")
	require.NotEqual(t, a, c, "different content must change the signature")
	require.Len(t, a, len("sha256:"+strings.Repeat("0", 64)))
}

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		name string
		log  string
		ctx  Context
		want string
		conf func(t *testing.T, c float64)
	}{
		{
			name: "security violation",
			log:  "error: secret detected in diff: AKIAIOSFODNN7 (policy violation)",
			want: ClassSecurityPolicyViolation,
			conf: above(0.9),
		},
		{
			name: "merge conflict",
			log:  "CONFLICT (content): Merge conflict in services/cart/cart.go\nAutomatic merge failed; fix conflicts and then commit the result.",
			want: ClassMergeConflict,
			conf: above(0.9),
		},
		{
			name: "compile regression",
			log:  "services/cart/totals.go:11:2: undefined: Totals",
			want: ClassCompileRegression,
			conf: above(0.9),
		},
		{
			name: "expectation drift via golden",
			log:  "snapshot mismatch for CheckoutSummary; golden file outdated (-want +got)",
			want: ClassTestExpectationDrift,
			conf: above(0.8),
		},
		{
			name: "infra transient OOM kill",
			log:  "signal: killed\nexit status 137 oom-killer invoked",
			want: ClassInfraTransient,
			conf: above(0.85),
		},
		{
			name: "infra transient network beats generic timeout wording",
			log:  "dial tcp 10.0.0.5:5432: connection timed out after retries",
			want: ClassInfraTransient,
			conf: above(0.85),
		},
		{
			name: "product timeout is distinct from regression",
			log:  "panic: test timed out after 10m0s running tests:\nTestLongCheckout",
			want: ClassTimeout,
			conf: above(0.85),
		},
		{
			name: "budget deadline exceeded",
			log:  "context deadline exceeded while waiting for preview env",
			want: ClassTimeout,
			conf: above(0.85),
		},
		{
			name: "known flake by name",
			log:  "race detector detected data race\n--- FAIL: TestCacheStampede",
			ctx:  Context{KnownFlakes: []string{"TestCacheStampede"}},
			want: ClassKnownFlake,
			conf: above(0.9),
		},
		{
			name: "probable flake corroborated across environments",
			log:  "intermittent failure: passed on retry",
			ctx:  Context{CorroboratedReruns: 3, DistinctEnvironments: 2},
			want: ClassProbableFlake,
			conf: above(0.8),
		},
		{
			name: "uncorroborated flaky signal stays below escalation floor",
			log:  "flaky behavior observed once",
			want: ClassProbableFlake,
			conf: below(EscalationConfidenceFloor),
		},
		{
			name: "functional regression default",
			log:  "--- FAIL: TestTotals (0.02s)\n    expected 100 got 104",
			want: ClassFunctionalRegression,
			conf: above(0.8),
		},
		{
			name: "unclassifiable garbage falls to low-confidence escalation",
			log:  "weird output with no recognizable markers at all",
			want: ClassFunctionalRegression,
			conf: below(EscalationConfidenceFloor),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.log, tc.ctx)
			require.Equal(t, tc.want, got.Classification)
			tc.conf(t, got.ClassificationConfidence)
			require.NotEmpty(t, got.SignatureDigest)
			require.True(t, strings.HasPrefix(got.SignatureDigest, "sha256:"))
		})
	}
}

func above(min float64) func(*testing.T, float64) {
	return func(t *testing.T, c float64) { require.GreaterOrEqual(t, c, min) }
}

func below(max float64) func(*testing.T, float64) {
	return func(t *testing.T, c float64) { require.Less(t, c, max) }
}

func TestClassifyCausalSignalsAndPaths(t *testing.T) {
	log := "services/cart/cart.go:1: compile error\n--- FAIL: TestTotals\nexit status 137"
	got := Classify(log, Context{})
	require.Equal(t, "compile_error", got.RuleID, "first decisive rule wins")
	require.Equal(t, []string{"compile_error", "infra_failure", "assertion_failure"}, got.CausalSignals,
		"all matched rules are recorded in evaluation order")
	require.Contains(t, got.SuspectedPaths, "services/cart/cart.go")
	require.GreaterOrEqual(t, len(got.SuspectedPaths), 1)
}

func TestReproductionCommandExtraction(t *testing.T) {
	got := ReproductionCommand(logCart)
	require.Equal(t, "go test sauron.dev/sauron/control-plane/services/cart -run '^TestTotals$' -count=1", got)

	shell := "$ make test-integration\nall good"
	require.Equal(t, "make test-integration", ReproductionCommand(shell))

	require.Empty(t, ReproductionCommand("nothing here"))
}

// --- property tests ---

var logWord = rapid.SampledFrom([]string{
	"FAIL:", "ok", "panic:", "timeout", "connection refused", "undefined:",
	"services/x/y.go:12", "expected 10 got 20", "2026-08-23T00:00:00Z", "0.25ms",
})

func TestPropertyClassificationTotalAndBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(logWord, 0, 12).Draw(t, "log_words")
		fc := Classify(strings.Join(parts, " "), Context{
			CorroboratedReruns:   rapid.IntRange(0, 5).Draw(t, "reruns"),
			DistinctEnvironments: rapid.IntRange(0, 5).Draw(t, "envs"),
		})
		switch fc.Classification {
		case ClassInfraTransient, ClassKnownFlake, ClassProbableFlake, ClassCompileRegression,
			ClassTestExpectationDrift, ClassFunctionalRegression, ClassMergeConflict,
			ClassSecurityPolicyViolation, ClassTimeout:
		default:
			t.Fatalf("classification %q outside §1.7 taxonomy", fc.Classification)
		}
		require.GreaterOrEqual(t, fc.ClassificationConfidence, 0.0)
		require.LessOrEqual(t, fc.ClassificationConfidence, 1.0)

		again := Classify(strings.Join(parts, " "), Context{})
		require.Equal(t, fc.SignatureDigest, again.SignatureDigest)
	})
}

func TestPropertyNumericPerturbationKeepsDigest(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := "line one value=100 duration=32ms at 10:00:00 FAIL"
		n := rapid.IntRange(1, 999999).Draw(t, "n")
		perturbed := fmt.Sprintf("line one value=%d duration=%dms at 10:%02d:%02d FAIL", n, n%97, n%60, n%60)
		require.Equal(t, SignatureDigest(base), SignatureDigest(perturbed),
			"numeric perturbations must not change the signature digest")
	})
}

func testPolicy(level int, repairClasses []string) policy.ResolvedPolicy {
	rec := policy.DefaultPolicyPack()
	rp := policy.ResolvedPolicy{PolicyID: rec.ID, Version: rec.Version, Body: rec.Body}
	rp.Body.Autonomy.Level = level
	if repairClasses != nil {
		rp.Body.Autonomy.AutoRepairClasses = repairClasses
	}
	return rp
}

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
