package evidence

import "strconv"

// TestResults is the runner-reported outcome census carried on completions
// (internal-protocols §2 results object). It is the P0-2 fix for the I-01
// bypass: verdicts must trace to REAL executed outcomes instead of being
// synthesized from the job status alone.
type TestResults struct {
	Total       int `json:"total"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Quarantined int `json:"quarantined"`
}

// Executed counts tests that actually ran to a pass/fail ruling; skipped,
// quarantined and filtered outcomes are non-evidence by I-01.
func (r TestResults) Executed() int { return r.Passed + r.Failed }

// Sum returns the total of all counted outcomes; consistency requires it to
// equal Total exactly.
func (r TestResults) Sum() int {
	return r.Passed + r.Failed + r.Skipped + r.Quarantined
}

// Valid reports whether the census is internally consistent and non-negative.
func (r TestResults) Valid() bool {
	if r.Total < 0 || r.Passed < 0 || r.Failed < 0 || r.Skipped < 0 || r.Quarantined < 0 {
		return false
	}
	return r.Sum() == r.Total
}

// checkResults enforces the P0-2/I-01 outcome-census rules when real results
// are present: counts must be consistent, executed failures contradict pass
// verdicts, and zero-executed (all-skip/quarantine) or skip-only outcomes
// can never back a pass verdict.
func checkResults(p ProposedRecord) (Evaluation, bool) {
	if p.Results == nil {
		return Evaluation{}, true // legacy callers without census keep string semantics
	}
	r := *p.Results
	if !r.Valid() {
		return Evaluation{Action: ActionReject, Reason: ReasonMalformed}, false
	}
	if r.Executed() == 0 {
		reason := ReasonNoExecutedTests
		if r.Skipped > 0 || r.Quarantined > 0 {
			reason = ReasonSkipNeverPositive
		}
		return Evaluation{Action: ActionReject, Reason: reason}, false
	}
	if p.Verdict == VerdictPass && r.Failed > 0 {
		return Evaluation{Action: ActionReject, Reason: ReasonVerdictUnsupported}, false
	}
	if p.Verdict == VerdictFail && r.Failed == 0 {
		return Evaluation{Action: ActionReject, Reason: ReasonVerdictUnsupported}, false
	}
	return Evaluation{}, true
}

// resultsMeta annotates accepted records whose census contains non-evidence
// outcomes so dossiers can show exactly what was excluded and why.
func resultsMeta(p ProposedRecord) map[string]string {
	if p.Results == nil || p.Results.Skipped == 0 {
		return nil
	}
	return map[string]string{"skipped_as_non_evidence": strconv.Itoa(p.Results.Skipped)}
}

func checkWellFormed(p ProposedRecord) (string, bool) {
	if p.ID == "" || p.RunID == "" || p.CandidateID == "" || p.Kind == "" || p.Attempt < 1 {
		return ReasonMalformed, false
	}
	if p.Verdict != VerdictPass && p.Verdict != VerdictFail {
		return ReasonMalformed, false
	}
	for _, d := range p.Digests {
		if !isSHA256(d) {
			return ReasonMalformed, false
		}
	}
	if p.InputsHash != "" && !isSHA256(p.InputsHash) {
		return ReasonMalformed, false
	}
	return "", true
}
