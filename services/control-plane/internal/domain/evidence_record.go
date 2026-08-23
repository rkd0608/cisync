package domain

import (
	"fmt"
	"time"
)

// EvidenceStatus is the acceptance lifecycle of an evidence record.
type EvidenceStatus string

// EvidenceRecord statuses (DOMAIN_MODEL_DRAFT §1.6).
const (
	EvidenceProposed    EvidenceStatus = "proposed"
	EvidenceAccepted    EvidenceStatus = "accepted"
	EvidenceRejected    EvidenceStatus = "rejected"
	EvidenceInvalidated EvidenceStatus = "invalidated"
)

var evidenceTerminalStates = map[EvidenceStatus]bool{
	EvidenceRejected: true, EvidenceInvalidated: true,
}

// Terminal reports whether the status is terminal (I-08).
func (s EvidenceStatus) Terminal() bool { return evidenceTerminalStates[s] }

// EvidenceRecord is one unit of validation evidence bound to a run attempt
// (invariants I-01/I-02/I-03).
type EvidenceRecord struct {
	ID                string
	TenantID          string
	RunID             string
	Attempt           int
	CandidateID       string
	Kind              string
	Verdict           string
	Status            EvidenceStatus
	Digests           []string
	InputsHash        string
	Confidence        float64
	CostMillicents    int64
	ProducedByLease   string
	AcceptedAt        *time.Time
	InvalidatedReason string
	CreatedAt         time.Time
}

// NewEvidenceRecord constructs a proposed evidence record.
func NewEvidenceRecord(id, tenantID, runID string, attempt int, candidateID, kind, verdict string, digests []string, inputsHash string, confidence float64, cost int64, leaseID string, now time.Time) *EvidenceRecord {
	return &EvidenceRecord{
		ID: id, TenantID: tenantID, RunID: runID, Attempt: attempt,
		CandidateID: candidateID, Kind: kind, Verdict: verdict,
		Status: EvidenceProposed, Digests: digests, InputsHash: inputsHash,
		Confidence: confidence, CostMillicents: cost, ProducedByLease: leaseID,
		CreatedAt: now,
	}
}

var evidenceTransitions = map[string]transitionRule{
	"evidence.accepted":    {from: []string{string(EvidenceProposed)}, to: string(EvidenceAccepted)},
	"evidence.rejected":    {from: []string{string(EvidenceProposed)}, to: string(EvidenceRejected)},
	"evidence.invalidated": {from: []string{string(EvidenceAccepted)}, to: string(EvidenceInvalidated)},
}

// Apply advances the evidence record's state machine on the named trigger.
// Terminal records log-and-ignore every further event (I-08); accepted
// records are never deleted, invalidation is a state.
func (e *EvidenceRecord) Apply(trigger string) error {
	if e.Status.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := evidenceTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for evidence", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(e.Status)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, e.ID, e.Status, trigger)
	}
	if trigger == "evidence.accepted" {
		now := time.Now().UTC()
		e.AcceptedAt = &now
	}
	if trigger == "evidence.invalidated" && e.InvalidatedReason == "" {
		e.InvalidatedReason = "unspecified"
	}
	e.Status = EvidenceStatus(rule.to)
	return nil
}

// Invalidate marks an accepted record invalidated with an explicit reason.
func (e *EvidenceRecord) Invalidate(reason string) error {
	if err := e.Apply("evidence.invalidated"); err != nil {
		return err
	}
	e.InvalidatedReason = reason
	return nil
}
