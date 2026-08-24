package evidence

import (
	"testing"
)

// P0-2 / I-01 regression: verdicts must trace to REAL executed outcomes.
// A runner that reports all-skips with status=succeeded can no longer mint
// pass evidence, and partial skips are recorded as non-evidence.

func validResults() TestResults {
	return TestResults{Total: 8, Passed: 8}
}

func baseProposalWithResults(results TestResults) ProposedRecord {
	return ProposedRecord{
		ID:          "ev_1",
		RunID:       "run_1",
		CandidateID: "cand_1",
		Kind:        "selected_unit",
		Verdict:     VerdictPass,
		Digests:     []string{"sha256:" + hexOf("ef")},
		InputsHash:  "sha256:" + hexOf("ab"),
		LeaseJTI:    "lease_1",
		Attempt:     1,
		Outcome:     OutcomePassed,
		Results:     &results,
	}
}

func ctxFor() Context {
	return Context{
		ExpectedLeaseJTI:   "lease_1",
		ExpectedInputsHash: "sha256:" + hexOf("ab"),
	}
}

func TestAllSkippedSucceededIsRejectedNotPassEvidence(t *testing.T) {
	results := TestResults{Total: 10, Skipped: 10}
	ruling := Evaluate(baseProposalWithResults(results), ctxFor())
	if ruling.Action != ActionReject {
		t.Fatalf("all-skipped outcome must never be pass evidence: %+v", ruling)
	}
	if ruling.Reason != ReasonSkipNeverPositive {
		t.Fatalf("reason must be the I-01 reason, got %q", ruling.Reason)
	}
}

func TestQuarantinedOnlyOutcomeRejected(t *testing.T) {
	results := TestResults{Total: 5, Quarantined: 5}
	if ruling := Evaluate(baseProposalWithResults(results), ctxFor()); ruling.Action != ActionReject || ruling.Reason != ReasonSkipNeverPositive {
		t.Fatalf("quarantined-only outcome must reject with I-01 reason: %+v", ruling)
	}
}

func TestZeroExecutedTestsCannotSatisfySufficiency(t *testing.T) {
	results := TestResults{Total: 0}
	ruling := Evaluate(baseProposalWithResults(results), ctxFor())
	if ruling.Action != ActionReject {
		t.Fatalf("zero-executed outcome cannot satisfy sufficiency: %+v", ruling)
	}
	if ruling.Reason != ReasonNoExecutedTests {
		t.Fatalf("expected zero-executed reason, got %q", ruling.Reason)
	}
}

func TestPartiallySkippedAcceptedWithMeta(t *testing.T) {
	results := TestResults{Total: 10, Passed: 7, Skipped: 3}
	ruling := Evaluate(baseProposalWithResults(results), ctxFor())
	if ruling.Action != ActionAccept {
		t.Fatalf("executed passes with partial skips must accept: %+v", ruling)
	}
	if ruling.Meta["skipped_as_non_evidence"] != "3" {
		t.Fatalf("meta must record skipped_as_non_evidence=3, got %v", ruling.Meta)
	}
}

func TestFailedResultContradictsPassVerdict(t *testing.T) {
	results := TestResults{Total: 6, Passed: 4, Failed: 2}
	ruling := Evaluate(baseProposalWithResults(results), ctxFor())
	if ruling.Action != ActionReject || ruling.Reason != ReasonVerdictUnsupported {
		t.Fatalf("failed>0 contradicts pass verdict: %+v", ruling)
	}
}

func TestFailVerdictRequiresExecutedFailure(t *testing.T) {
	p := baseProposalWithResults(TestResults{Total: 4, Failed: 4})
	p.Verdict = VerdictFail
	p.Outcome = OutcomeFailed
	if ruling := Evaluate(p, ctxFor()); ruling.Action != ActionAccept {
		t.Fatalf("honest fail evidence must accept: %+v", ruling)
	}

	noFailure := baseProposalWithResults(TestResults{Total: 4, Passed: 4})
	noFailure.Verdict = VerdictFail
	noFailure.Outcome = OutcomeFailed
	if ruling := Evaluate(noFailure, ctxFor()); ruling.Action == ActionAccept {
		t.Fatal("fail verdict without executed failures must not pass validation")
	}
}

func TestInconsistentCountsAreMalformed(t *testing.T) {
	results := TestResults{Total: 5, Passed: 9}
	if ruling := Evaluate(baseProposalWithResults(results), ctxFor()); ruling.Reason != ReasonMalformed {
		t.Fatalf("counts exceeding total are malformed: %+v", ruling)
	}
	negative := TestResults{Total: -1}
	if ruling := Evaluate(baseProposalWithResults(negative), ctxFor()); ruling.Reason != ReasonMalformed {
		t.Fatalf("negative counts are malformed: %+v", ruling)
	}
}

func TestLegacyNilResultsKeepsStringOutcomePath(t *testing.T) {
	p := ProposedRecord{
		ID: "ev_legacy", RunID: "run_l", CandidateID: "cand_l",
		Kind: "selected_unit", Verdict: VerdictPass,
		Digests:    []string{"sha256:" + hexOf("ef")},
		InputsHash: "sha256:" + hexOf("ab"),
		LeaseJTI:   "lease_1", Attempt: 1, Outcome: OutcomePassed,
	}
	if ruling := Evaluate(p, ctxFor()); ruling.Action != ActionAccept {
		t.Fatalf("nil Results keeps the legacy string-outcome path: %+v", ruling)
	}
}
