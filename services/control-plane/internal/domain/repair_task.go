package domain

import (
	"fmt"
	"time"
)

// RepairState is the lifecycle state of a repair task.
type RepairState string

// RepairTask states (DOMAIN_MODEL_DRAFT §1.9).
const (
	RepairAuthorized RepairState = "authorized"
	RepairDispatched RepairState = "dispatched"
	RepairIterating  RepairState = "iterating"
	RepairApplied    RepairState = "applied"
	RepairExhausted  RepairState = "exhausted"
	RepairAborted    RepairState = "aborted"
)

var repairTerminalStates = map[RepairState]bool{
	RepairApplied: true, RepairExhausted: true, RepairAborted: true,
}

// Terminal reports whether the state is terminal (I-08).
func (s RepairState) Terminal() bool { return repairTerminalStates[s] }

// RepairEnvelope bounds what a repair agent may do (invariant I-05).
type RepairEnvelope struct {
	ReproductionCommand         string   `json:"reproduction_command"`
	FailedAssertion             string   `json:"failed_assertion,omitempty"`
	SuspectedDiffHunks          []string `json:"suspected_diff_hunks,omitempty"`
	AllowedPaths                []string `json:"allowed_paths"`
	ProhibitedPaths             []string `json:"prohibited_paths,omitempty"`
	MaxIterations               int      `json:"max_iterations"`
	RequiredEvidenceAfterRepair []string `json:"required_evidence_after_repair,omitempty"`
}

// RepairTask is one bounded repair authorization against a failure case.
type RepairTask struct {
	ID                 string
	TenantID           string
	FailureCaseID      string
	CandidateID        string
	State              RepairState
	Envelope           RepairEnvelope
	AttemptsUsed       int
	ResultingPatchRefs []string
	CreatedAt          time.Time
}

// NewRepairTask constructs an authorized repair task.
func NewRepairTask(id, tenantID, failureCaseID, candidateID string, env RepairEnvelope, now time.Time) *RepairTask {
	return &RepairTask{
		ID: id, TenantID: tenantID, FailureCaseID: failureCaseID,
		CandidateID: candidateID, State: RepairAuthorized, Envelope: env,
		CreatedAt: now,
	}
}

var repairTransitions = map[string]transitionRule{
	"repair.dispatched": {from: []string{string(RepairAuthorized)}, to: string(RepairDispatched)},
	"repair.iterating":  {from: []string{string(RepairDispatched), string(RepairIterating)}, to: string(RepairIterating)},
	"repair.applied":    {from: []string{string(RepairDispatched), string(RepairIterating)}, to: string(RepairApplied)},
	"repair.exhausted":  {from: []string{string(RepairDispatched), string(RepairIterating)}, to: string(RepairExhausted)},
	"repair.aborted":    {from: []string{string(RepairAuthorized), string(RepairDispatched), string(RepairIterating)}, to: string(RepairAborted)},
}

// Apply advances the repair task's state machine on the named trigger.
// Terminal tasks log-and-ignore every further event (I-08); attempts_used is
// monotonic.
func (t *RepairTask) Apply(trigger string) error {
	if t.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := repairTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for repair_task", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(t.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, t.ID, t.State, trigger)
	}
	t.State = RepairState(rule.to)
	return nil
}

// RecordAttempt bumps attempts_used and exhausts the task when the bounded
// iteration cap is reached.
func (t *RepairTask) RecordAttempt() error {
	if err := t.Apply("repair.iterating"); err != nil && t.State != RepairDispatched && t.State != RepairIterating {
		return err
	}
	if t.AttemptsUsed >= t.Envelope.MaxIterations {
		return t.Apply("repair.exhausted")
	}
	t.AttemptsUsed++
	if t.AttemptsUsed >= t.Envelope.MaxIterations {
		return t.Apply("repair.exhausted")
	}
	return nil
}
