package failure

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
