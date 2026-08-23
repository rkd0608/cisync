package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// InputsHash derives the full evidence-reuse key over base SHA, head SHA and
// patch ref (invariant I-02). The selection planner computes a richer key
// over lockfiles/flags/toolchain; this variant stays available for doubles.
func InputsHash(baseSHA, headSHA, patchRef string) string {
	h := sha256.Sum256([]byte(baseSHA + "\x00" + headSHA + "\x00" + patchRef))
	return HashPrefix + hex.EncodeToString(h[:])
}

// PlanState is the lifecycle state of a validation plan.
type PlanState string

// ValidationPlan states (DOMAIN_MODEL_DRAFT §1.4).
const (
	PlanActive      PlanState = "active"
	PlanSatisfied   PlanState = "satisfied"
	PlanInvalidated PlanState = "invalidated"
	PlanSuperseded  PlanState = "superseded"
)

var planTerminalStates = map[PlanState]bool{PlanSatisfied: true, PlanSuperseded: true}

// Terminal reports whether the state is terminal (I-08).
func (s PlanState) Terminal() bool { return planTerminalStates[s] }

// Tier is one rung of the validation ladder inside a plan.
type Tier struct {
	Tier                int      `json:"tier"`
	Jobs                []string `json:"jobs"`
	Rationale           string   `json:"rationale"`
	SelectionConfidence *float64 `json:"selection_confidence,omitempty"`
}

// ValidationPlan is the tiered evidence plan built for a candidate; it cites
// its resolved policy per I-09.
type ValidationPlan struct {
	ID                    string
	TenantID              string
	CandidateID           string
	State                 PlanState
	Tiers                 []Tier
	RequiredEvidenceKinds []string
	Policy                PolicyRef
	InputsHash            string
	CreatedAt             time.Time
}

// NewValidationPlan constructs a plan in the active state.
func NewValidationPlan(id, tenantID, candidateID string, tiers []Tier, requiredKinds []string, pol PolicyRef, inputsHash string, now time.Time) *ValidationPlan {
	return &ValidationPlan{
		ID: id, TenantID: tenantID, CandidateID: candidateID, State: PlanActive,
		Tiers: tiers, RequiredEvidenceKinds: requiredKinds, Policy: pol,
		InputsHash: inputsHash, CreatedAt: now,
	}
}

var planTransitions = map[string]transitionRule{
	"plan.satisfied":       {from: []string{string(PlanActive)}, to: string(PlanSatisfied)},
	"evidence.invalidated": {from: []string{string(PlanActive)}, to: string(PlanInvalidated)},
	"plan.rebuilt":         {from: []string{string(PlanInvalidated)}, to: string(PlanActive)},
	"plan.superseded":      {from: []string{string(PlanActive), string(PlanInvalidated)}, to: string(PlanSuperseded)},
}

// Apply advances the plan's state machine on the named trigger.
// Terminal aggregates log-and-ignore every further event (I-08).
func (p *ValidationPlan) Apply(trigger string) error {
	if p.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := planTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for validation_plan", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(p.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, p.ID, p.State, trigger)
	}
	p.State = PlanState(rule.to)
	return nil
}
