package failure

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
	"cisync.dev/cisync/control-plane/internal/policy"
)

// Shared fixtures for classify and repair scenario suites.
const logCart = `2026-08-23T03:41:00Z INFO  runner worker=7 pid=2214
--- FAIL: TestTotals (0.02s)
    cart_test.go:42: expected 100 got 104
FAIL	cisync.dev/cisync/control-plane/services/cart	0.512s
`

func above(min float64) func(*testing.T, float64) {
	return func(t *testing.T, c float64) { require.GreaterOrEqual(t, c, min) }
}

func below(max float64) func(*testing.T, float64) {
	return func(t *testing.T, c float64) { require.Less(t, c, max) }
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
