package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var reportAt = time.Date(2026, 8, 26, 9, 30, 0, 0, time.UTC)

func goldenReportDossier() *ReportDossier {
	return &ReportDossier{
		EvidenceRows: []EvidenceRow{
			{Kind: "hermetic_build", Tier: 1, Verdict: "accepted", Executed: 1, Skipped: 0, DurationMS: 42000},
			{Kind: "selected_unit", Tier: 2, Verdict: "deferred", Executed: 44, Skipped: 1842, DurationMS: 61000},
		},
		Skipped: &SkippedNonEvidence{
			Total:     2395,
			Rationale: "Do not discriminate candidate quality (VCS churn sets, tier-ineligible classes)",
		},
		Failures: []FailureCaseReport{
			{
				Kind: "api_compat", Classification: "deterministic_regression",
				Confidence: 0.91, ReproductionCommand: "bazel test //payments/checkout:retry_contract_test",
				RoutedAction: "scoped_repair", RepairAttempt: 1, RepairMax: 2,
			},
		},
		Timeline: []TimelineEvent{
			{At: reportAt.Add(-3 * time.Minute), Event: "candidate.submitted"},
			{At: reportAt.Add(-time.Minute), Event: "tier1.completed"},
			{At: reportAt, Event: "decision.rendered"},
		},
	}
}

func TestDecisionEnvelopeAcceptsPRNumberAndDossier(t *testing.T) {
	env := DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    VerbEligibleForMergeTrain, Confidence: 0.94,
		Policy: PolicyRef{PolicyID: "pol_x", Version: 4}, RenderedAt: reportAt,
		PRNumber: 17, Report: goldenReportDossier(),
	}
	require.NoError(t, env.Validate())
	require.Equal(t, 17, env.PRNumber)
}

func TestDecisionEnvelopeReportBlockRejected(t *testing.T) {
	base := func(report *ReportDossier) DecisionEnvelope {
		return DecisionEnvelope{
			DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
			HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Verb:    VerbEligibleForMergeTrain, Confidence: 0.5,
			Policy: PolicyRef{PolicyID: "pol_x", Version: 1}, RenderedAt: reportAt,
			Report: report,
		}
	}
	cases := map[string]*ReportDossier{
		"bad_verdict": {EvidenceRows: []EvidenceRow{{Kind: "k", Verdict: "passed"}}},
		"empty_kind":  {EvidenceRows: []EvidenceRow{{Verdict: VerdictAccepted}}},
		"negative_counts": {EvidenceRows: []EvidenceRow{
			{Kind: "k", Verdict: VerdictAccepted, Executed: -1}}},
		"skipped_rationale_required": {Skipped: &SkippedNonEvidence{Total: 5}},
		"failure_missing_classification": {Failures: []FailureCaseReport{
			{Kind: "k", RoutedAction: "repair"}}},
		"failure_confidence_out_of_range": {Failures: []FailureCaseReport{
			{Kind: "k", Classification: "c", Confidence: 1.5, RoutedAction: "repair"}}},
		"timeline_zero_at": {Timeline: []TimelineEvent{{Event: "candidate.submitted"}}},
	}
	for name, report := range cases {
		env := base(report)
		require.Error(t, env.Validate(), "case %s must fail closed", name)
	}
}

func TestDecisionEnvelopeRejectsNegativePRNumber(t *testing.T) {
	env := DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    VerbRejected, Policy: PolicyRef{PolicyID: "pol_x", Version: 1},
		RenderedAt: reportAt, PRNumber: -3,
	}
	require.Error(t, env.Validate())
}

func TestDecisionWireRoundTripKeepsOptionalBlocks(t *testing.T) {
	in := DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    VerbRejected, Confidence: 0.87,
		Policy: PolicyRef{PolicyID: "pol_x", Version: 2}, RenderedAt: reportAt,
		PRNumber: 42, Report: goldenReportDossier(),
	}
	raw, err := json.Marshal(&in)
	require.NoError(t, err)
	var out DecisionEnvelope
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, 42, out.PRNumber)
	require.NotNil(t, out.Report)
	require.Len(t, out.Report.EvidenceRows, 2)
	require.Len(t, out.Report.Failures, 1)
	require.Len(t, out.Report.Timeline, 3)
}

// Pre-widening relays omit pr_number/report entirely — decoding stays valid.
func TestLegacyDecisionWithoutReportStillValid(t *testing.T) {
	env := DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    VerbDeferred, Policy: PolicyRef{PolicyID: "pol_x", Version: 1},
		RenderedAt: reportAt,
	}
	require.NoError(t, env.Validate())
	require.Zero(t, env.PRNumber)
	require.Nil(t, env.Report)
}
