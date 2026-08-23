// Package evidence evaluates proposed evidence records at accept-time and
// computes dossier sufficiency. It enforces the structural evidence
// invariants: I-01 (a skipped/quarantined outcome never counts as positive
// evidence), I-02 (full inputs_hash equality required) and I-03 (at most one
// accepted record per (run_id, attempt) and per lease jti).
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Runner-reported test outcomes understood by the validator.
const (
	OutcomePassed      = "passed"
	OutcomeFailed      = "failed"
	OutcomeSkipped     = "skipped"
	OutcomeQuarantined = "quarantined"
	OutcomeFiltered    = "filtered"
)

// Verdicts carried on an evidence record.
const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

// Action is the ruling Evaluate returns.
type Action string

// Rulings.
const (
	ActionAccept     Action = "accept"
	ActionReject     Action = "reject"
	ActionQuarantine Action = "quarantine" // tamper suspected; also raises a security alert upstream (EC-038)
)

// Machine-readable rejection reasons, in evaluation-precedence order.
const (
	ReasonMalformed              = "malformed_record"
	ReasonProvenanceMismatch     = "lease_provenance_mismatch"
	ReasonInputsHashMismatch     = "inputs_hash_mismatch"                    // I-02
	ReasonDuplicateAttempt       = "duplicate_run_attempt"                   // I-03a
	ReasonLeaseAlreadyAccepted   = "lease_jti_already_accepted"              // I-03b
	ReasonSkipNeverPositive      = "skip_quarantine_never_positive_evidence" // I-01
	ReasonVerdictUnsupported     = "verdict_not_supported_by_outcome"
	ReasonDigestManifestMismatch = "digest_manifest_mismatch"
)

// ProposedRecord is a runner-submitted evidence record awaiting validation.
type ProposedRecord struct {
	ID          string
	RunID       string
	CandidateID string
	Kind        string
	Verdict     string   // pass|fail as claimed by the submitter
	Outcome     string   // raw runner outcome for the underlying tests
	Digests     []string // artifact digests, each "sha256:<64 hex>"
	InputsHash  string   // full reuse key: base SHA + lockfiles + flags + toolchain
	LeaseJTI    string   // producing job lease identity
	Attempt     int
}

// AcceptedRef is one already-accepted record's uniqueness keys (I-03).
type AcceptedRef struct {
	RunID    string
	Attempt  int
	LeaseJTI string
}

// Context binds the proposal to its provenance expectations.
type Context struct {
	ExpectedLeaseJTI   string        // lease this run attempt was issued under
	ExpectedInputsHash string        // plan-level inputs_hash (I-02)
	ExpectedDigests    []string      // optional manifest; nil disables deep tamper check
	Accepted           []AcceptedRef // prior accepted records for uniqueness checks
}

// Evaluation is the deterministic ruling for one proposal.
type Evaluation struct {
	Action Action
	Reason string // empty when accepted
}

// Evaluate validates one proposed record against its acceptance context.
// Check order is fixed: structural validity, lease provenance, I-02 inputs
// hash equality, I-03 uniqueness, I-01 outcome admissibility, then manifest
// tamper detection.
func Evaluate(p ProposedRecord, ctx Context) Evaluation {
	if reason, ok := checkWellFormed(p); !ok {
		return Evaluation{Action: ActionReject, Reason: reason}
	}
	if ctx.ExpectedLeaseJTI == "" || p.LeaseJTI != ctx.ExpectedLeaseJTI {
		return Evaluation{Action: ActionReject, Reason: ReasonProvenanceMismatch}
	}
	if p.InputsHash != ctx.ExpectedInputsHash || ctx.ExpectedInputsHash == "" {
		return Evaluation{Action: ActionReject, Reason: ReasonInputsHashMismatch}
	}
	for _, ref := range ctx.Accepted {
		if ref.RunID == p.RunID && ref.Attempt == p.Attempt {
			return Evaluation{Action: ActionReject, Reason: ReasonDuplicateAttempt}
		}
	}
	for _, ref := range ctx.Accepted {
		if p.LeaseJTI != "" && ref.LeaseJTI == p.LeaseJTI {
			return Evaluation{Action: ActionReject, Reason: ReasonLeaseAlreadyAccepted}
		}
	}
	switch p.Outcome {
	case OutcomeSkipped, OutcomeQuarantined, OutcomeFiltered:
		return Evaluation{Action: ActionReject, Reason: ReasonSkipNeverPositive}
	case OutcomePassed:
		if p.Verdict != VerdictPass {
			return Evaluation{Action: ActionReject, Reason: ReasonVerdictUnsupported}
		}
	case OutcomeFailed:
		if p.Verdict != VerdictFail {
			return Evaluation{Action: ActionReject, Reason: ReasonVerdictUnsupported}
		}
	default:
		return Evaluation{Action: ActionReject, Reason: ReasonVerdictUnsupported}
	}
	if ctx.ExpectedDigests != nil && !sameDigestSet(p.Digests, ctx.ExpectedDigests) {
		return Evaluation{Action: ActionQuarantine, Reason: ReasonDigestManifestMismatch}
	}
	return Evaluation{Action: ActionAccept}
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

func isSHA256(s string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	hexPart := strings.TrimPrefix(s, prefix)
	if len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func sameDigestSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sortStrings(as)
	sortStrings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
