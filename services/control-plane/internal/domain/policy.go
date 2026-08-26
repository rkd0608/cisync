package domain

import "time"

// PolicyStatus is the lifecycle state of a policy version.
type PolicyStatus string

// Policy states (DOMAIN_MODEL_DRAFT §1.10).
const (
	PolicyDraft   PolicyStatus = "draft"
	PolicyActive  PolicyStatus = "active"
	PolicyRetired PolicyStatus = "retired"
)

var policyTerminalStates = map[PolicyStatus]bool{PolicyRetired: true}

// Terminal reports whether the status is terminal (I-08).
func (s PolicyStatus) Terminal() bool { return policyTerminalStates[s] }

// ResolvedPolicy is the compiled-in default policy pack resolution used at
// every gate (invariant I-09: no resolvable active policy ⇒ fail closed).
type ResolvedPolicy struct {
	Ref                    PolicyRef           `json:"policy_ref"`
	Tier3RiskClasses       []string            `json:"tier3_risk_classes"`
	MinSelectionConfidence float64             `json:"min_selection_confidence"`
	ProtectedPaths         []string            `json:"protected_paths"`
	RequiredEvidenceByRisk map[string][]string `json:"required_evidence_by_risk"`
	PerCandidateBudget     BudgetValues        `json:"per_candidate_budget"`
	PerTenantHourCPU       int64               `json:"per_tenant_hour_cpu_minutes"`
	WIPByTier              map[int]int         `json:"wip_by_tier"`
}

// DefaultPolicy returns the v1 built-in default policy pack
// (DOMAIN_MODEL_DRAFT §8 shape, autonomy level 3 posture).
func DefaultPolicy() ResolvedPolicy {
	return ResolvedPolicy{
		Ref:                    PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
		Tier3RiskClasses:       []string{"high", "critical"},
		MinSelectionConfidence: 0.98,
		ProtectedPaths:         []string{"auth/**", "migrations/**", "infrastructure/prod/**"},
		RequiredEvidenceByRisk: map[string][]string{
			"low":      {"hermetic_build", "selected_unit"},
			"medium":   {"hermetic_build", "selected_unit", "api_compat"},
			"high":     {"hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"},
			"critical": {"hermetic_build", "api_compat", "full_integration", "human_approval"},
		},
		PerCandidateBudget: BudgetValues{CPUMinutes: 120, EnvironmentMinutes: 30, RepairAttempts: 2},
		PerTenantHourCPU:   5000,
		WIPByTier:          map[int]int{0: 200, 1: 60, 2: 20, 3: 6, 4: 2},
	}
}

// RequiredEvidence resolves the required evidence kinds for a risk class;
// unknown risk classes fail closed.
func (p ResolvedPolicy) RequiredEvidence(r RiskClass) ([]string, bool) {
	kinds, ok := p.RequiredEvidenceByRisk[string(r)]
	return kinds, ok
}

var policyTransitions = map[string]transitionRule{
	"policy.activated": {from: []string{string(PolicyDraft)}, to: string(PolicyActive)},
	"policy.retired":   {from: []string{string(PolicyActive)}, to: string(PolicyRetired)},
}

// Policy is a versioned policy document; ≤1 active version per family.
type Policy struct {
	ID          string
	Version     int
	Status      PolicyStatus
	Body        ResolvedPolicy
	ActivatedBy string
	ActivatedAt *time.Time
}

// Apply advances the policy's state machine on the named trigger. Active
// policies are immutable; amendments create a new version.
func (p *Policy) Apply(trigger string) error {
	if p.Status.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := policyTransitions[trigger]
	if !ok {
		return ErrUnknownEvent
	}
	if !matchesState(rule.from, string(p.Status)) {
		return ErrIllegalTransition
	}
	p.Status = PolicyStatus(rule.to)
	return nil
}
