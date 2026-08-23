package domain

import (
	"fmt"
	"time"
)

// RunState is the lifecycle state of a validation run (the job unit).
type RunState string

// ValidationRun states (DOMAIN_MODEL_DRAFT §1.5).
const (
	RunQueued     RunState = "queued"
	RunDispatched RunState = "dispatched"
	RunRunning    RunState = "running"
	RunSucceeded  RunState = "succeeded"
	RunFailed     RunState = "failed"
	RunTimedOut   RunState = "timed_out"
	RunCancelled  RunState = "cancelled"
)

var runTerminalStates = map[RunState]bool{
	RunSucceeded: true, RunCancelled: true,
}

// Terminal reports whether the state is terminal (I-08); failed is not
// terminal because bounded infra-transient retry re-queues it.
func (s RunState) Terminal() bool { return runTerminalStates[s] }

// JobSpec is the execution payload handed to runner-fleet
// (internal-protocols.md §3).
type JobSpec struct {
	Kind       string         `json:"kind"`
	Repo       string         `json:"repo"`
	BaseSHA    string         `json:"base_sha"`
	HeadSHA    string         `json:"head_sha"`
	PatchRef   string         `json:"patch_ref"`
	InputsHash string         `json:"inputs_hash"`
	TimeoutMS  int64          `json:"timeout_ms"`
	SimProfile map[string]any `json:"sim_profile,omitempty"`
}

// ValidationRun is one admitted validation unit dispatched to the fleet.
type ValidationRun struct {
	ID                string
	TenantID          string
	PlanID            string
	CandidateID       string
	State             RunState
	Tier              int
	JobSpec           JobSpec
	Attempt           int
	Pool              string
	EstDurationMS     int64
	EstCostMillicents int64
	Priority          float64
	FenceToken        int64
	TimeoutMS         int64
	DispatchedAt      *time.Time
	FinishedAt        *time.Time
	CreatedAt         time.Time
}

// NewValidationRun constructs a queued run with attempt=1 and fence_token=0.
func NewValidationRun(id, tenantID, planID, candidateID string, tier int, spec JobSpec, pool string, estDurationMS, estCost int64, priority float64, now time.Time) *ValidationRun {
	return &ValidationRun{
		ID: id, TenantID: tenantID, PlanID: planID, CandidateID: candidateID,
		State: RunQueued, Tier: tier, JobSpec: spec, Attempt: 1, Pool: pool,
		EstDurationMS: estDurationMS, EstCostMillicents: estCost,
		Priority: priority, TimeoutMS: spec.TimeoutMS, CreatedAt: now,
	}
}

var runTransitions = map[string]transitionRule{
	"run.dispatched": {from: []string{string(RunQueued)}, to: string(RunDispatched)},
	"run.claimed":    {from: []string{string(RunDispatched)}, to: string(RunRunning)},
	// WHY dispatched is accepted here: the completion feed is pull-based,
	// so control-plane may observe the terminal result without having seen
	// the intermediate claimed transition. Fencing still gates acceptance
	// (I-11); only non-terminal states may advance.
	"run.succeeded": {from: []string{string(RunRunning), string(RunDispatched)}, to: string(RunSucceeded)},
	"run.failed":    {from: []string{string(RunRunning), string(RunDispatched)}, to: string(RunFailed)},
	"run.timed_out": {from: []string{string(RunRunning), string(RunDispatched)}, to: string(RunTimedOut)},
	"run.retry":     {from: []string{string(RunFailed)}, to: string(RunQueued)},
	"run.cancelled": {from: []string{string(RunQueued), string(RunDispatched), string(RunRunning)}, to: string(RunCancelled)},
}

// Apply advances the run's state machine on the named trigger.
// Terminal aggregates log-and-ignore every further event (I-08); in
// particular cancel-after-complete is ignored.
func (r *ValidationRun) Apply(trigger string) error {
	if r.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := runTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for validation_run", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(r.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, r.ID, r.State, trigger)
	}
	switch trigger {
	case "run.retry":
		r.Attempt++
		r.FenceToken++
	case "run.dispatched", "run.claimed":
		now := time.Now().UTC()
		r.DispatchedAt = &now
	default:
		now := time.Now().UTC()
		r.FinishedAt = &now
	}
	if trigger == "run.cancelled" || trigger == "run.succeeded" || trigger == "run.failed" || trigger == "run.timed_out" {
		r.DispatchedAt = nil
	}
	r.State = RunState(rule.to)
	return nil
}
