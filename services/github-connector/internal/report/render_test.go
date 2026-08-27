package report

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
)

var (
	goldenNow     = time.Date(2026, 8, 26, 9, 30, 0, 0, time.UTC)
	goldenDetails = "http://localhost:3000"
)

func goldenEnv() domain.DecisionEnvelope {
	return domain.DecisionEnvelope{
		Kind: domain.KindDecision, DecisionID: "dec_01JREPORT",
		CandidateID: "cand_01JREPORT", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    domain.VerbEligibleForMergeTrain, Confidence: 0.94,
		Policy:     domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 4},
		Summary:    "All required evidence kinds accepted",
		RenderedAt: goldenNow, PRNumber: 17,
		Evidence: &domain.EvidenceCounts{Required: 5, Accepted: 5, Deferred: 2},
	}
}

// TestGoldenFullReport freezes the W6 comment body for the enriched push.
func TestGoldenFullReport(t *testing.T) {
	env := goldenEnv()
	env.Report = &domain.ReportDossier{
		EvidenceRows: []domain.EvidenceRow{
			{Kind: "hermetic_build", Tier: 1, Verdict: domain.VerdictAccepted,
				Executed: 1, Skipped: 0, DurationMS: 42000},
			{Kind: "selected_unit", Tier: 2, Verdict: domain.VerdictDeferred,
				Executed: 44, Skipped: 1842, DurationMS: 61000},
			{Kind: "api_compat", Tier: 3, Verdict: domain.VerdictFailed,
				Executed: 12, Skipped: 0, DurationMS: 8100},
		},
		Skipped: &domain.SkippedNonEvidence{
			Total:     2395,
			Rationale: "Do not discriminate candidate quality (VCS churn sets, tier-ineligible classes)",
		},
		Timeline: []domain.TimelineEvent{
			{At: goldenNow.Add(-90 * time.Second), Event: "tier2.completed"},
			{At: goldenNow.Add(-3 * time.Minute), Event: "candidate.submitted"},
			{At: goldenNow.Add(-2 * time.Minute), Event: "validation.planned"},
			{At: goldenNow, Event: "decision.rendered"},
		},
	}
	body, err := RenderComment(&env, goldenDetails)
	require.NoError(t, err)
	want := "<!-- cisync:report -->\n" +
		"## CISync Verification Report\n\n" +
		"**Eligible for merge train** · confidence 0.94 (moderate) · policy pol_cisync_default v4\n\n" +
		"All required evidence kinds accepted\n\n" +
		"### Evidence by kind\n" +
		"| kind | tier | verdict | executed | skipped | duration |\n" +
		"|---|---|---|---|---|---|\n" +
		"| hermetic_build | 1 | ✓ accepted | 1 | 0 | 42s |\n" +
		"| selected_unit | 2 | ○ deferred | 44 | 1842 | 61s |\n" +
		"| api_compat | 3 | ✗ failed | 12 | 0 | 8s |\n" +
		"\nAggregate census: 5 required · 5 accepted · 2 deferred (reason-linked) · 0 failed.\n\n" +
		"### Skipped as non-evidence\n" +
		"**2395** items skipped as non-evidence — Do not discriminate candidate quality " +
		"(VCS churn sets, tier-ineligible classes).\n\n" +
		"### Failures & Repairs\n" +
		"No failed-required evidence kinds.\n\n" +
		"### Decision timeline\n" +
		"- `2026-08-26T09:27:00Z` candidate.submitted\n" +
		"- `2026-08-26T09:28:00Z` validation.planned\n" +
		"- `2026-08-26T09:28:30Z` tier2.completed\n" +
		"- `2026-08-26T09:30:00Z` decision.rendered\n\n" +
		"> Full dossier: http://localhost:3000/candidates/cand_01JREPORT — live link\n"
	require.Equal(t, want, body)
}

// TestGoldenMultiFailureReport covers the failure dossier shape with repair
// envelopes and unicode inside the repro command / rationale strings.
func TestGoldenMultiFailureReport(t *testing.T) {
	env := goldenEnv()
	env.Verb = domain.VerbRejected
	env.Confidence = 0.87
	env.Summary = ""
	env.Evidence = &domain.EvidenceCounts{Required: 5, Accepted: 3, Deferred: 1, Failed: 1}
	env.PRNumber = 42
	env.Report = &domain.ReportDossier{
		Failures: []domain.FailureCaseReport{
			{
				Kind: "api_compat", Classification: "deterministic_regression",
				Confidence: 0.91, ReproductionCommand: "bazel test //payments/checkout:retry_contract_test",
				RoutedAction: "scoped_repair", RepairAttempt: 1, RepairMax: 2,
			},
			{
				Kind: "selected_unit", Classification: "test_expectation_drift",
				Confidence: 0.74, ReproductionCommand: "go test ./services/checkout -run TestRésumé多路复用 ✓",
				RoutedAction: "escalate_human",
			},
		},
		Skipped: &domain.SkippedNonEvidence{
			Total: 128, Rationale: "パス excluded by policy protected_paths",
		},
	}
	body, err := RenderComment(&env, goldenDetails)
	require.NoError(t, err)
	want := "<!-- cisync:report -->\n" +
		"## CISync Verification Report\n\n" +
		"**Rejected** · confidence 0.87 (moderate) · policy pol_cisync_default v4\n\n" +
		"### Evidence by kind\n" +
		"_per-kind breakdown not pushed by control-plane._\n\n" +
		"Aggregate census: 5 required · 3 accepted · 1 deferred (reason-linked) · 1 failed.\n\n" +
		"### Skipped as non-evidence\n" +
		"**128** items skipped as non-evidence — パス excluded by policy protected_paths.\n\n" +
		"### Failures & Repairs\n" +
		"1. **api_compat — deterministic_regression** (confidence 0.91, moderate) · routed: `scoped_repair` · repair attempt 1/2\n" +
		"   ```bash\n" +
		"   bazel test //payments/checkout:retry_contract_test\n" +
		"   ```\n" +
		"2. **selected_unit — test_expectation_drift** (confidence 0.74, low) · routed: `escalate_human` · no repair attempts spent\n" +
		"   ```bash\n" +
		"   go test ./services/checkout -run TestRésumé多路复用 ✓\n" +
		"   ```\n" +
		"\n### Decision timeline\n_timeline not pushed by control-plane._\n\n" +
		"> Full dossier: http://localhost:3000/candidates/cand_01JREPORT — live link\n"
	require.Equal(t, want, body)
}

// TestGoldenMinimalReport proves pre-W6 relays render every section in its
// degraded-but-honest form instead of empty headers.
func TestGoldenMinimalReport(t *testing.T) {
	env := goldenEnv()
	env.Report = nil
	env.Summary = "explanation summary"
	body, err := RenderComment(&env, goldenDetails)
	require.NoError(t, err)
	require.Contains(t, body, "_per-kind breakdown not pushed by control-plane._")
	require.Contains(t, body, "_skip rationale not pushed by control-plane._")
	require.Contains(t, body, "No failed-required evidence kinds.")
	require.Contains(t, body, "_timeline not pushed by control-plane._")
}

func TestRenderCommentRejectsUnsupportedVerb(t *testing.T) {
	env := goldenEnv()
	env.Verb = domain.DecisionVerb("ship_it")
	_, err := RenderComment(&env, goldenDetails)
	require.Error(t, err)
}
