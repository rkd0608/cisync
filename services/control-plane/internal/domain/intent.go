package domain

import (
	"fmt"
	"time"
)

// IntentState is the Change-Graph UI state of an intent.
type IntentState string

// Intent states (DOMAIN_MODEL_DRAFT §1.1).
const (
	IntentExploring  IntentState = "exploring"
	IntentValidating IntentState = "validating"
	IntentBlocked    IntentState = "blocked"
	IntentRepairing  IntentState = "repairing"
	IntentMergeReady IntentState = "merge_ready"
	IntentDeploying  IntentState = "deploying"
	IntentMonitoring IntentState = "monitoring"
	IntentCompleted  IntentState = "completed"
	IntentRejected   IntentState = "rejected"
)

var intentTerminalStates = map[IntentState]bool{
	IntentCompleted: true,
	IntentRejected:  true,
}

// Terminal reports whether the state is terminal (I-08: no resurrection).
func (s IntentState) Terminal() bool { return intentTerminalStates[s] }

// RiskClass classifies blast radius for policy resolution.
type RiskClass string

// Risk classes.
const (
	RiskLow      RiskClass = "low"
	RiskMedium   RiskClass = "medium"
	RiskHigh     RiskClass = "high"
	RiskCritical RiskClass = "critical"
)

// BudgetValues is the compute budget triple shared by intents and leases.
type BudgetValues struct {
	CPUMinutes         int64 `json:"cpu_minutes"`
	EnvironmentMinutes int64 `json:"environment_minutes"`
	RepairAttempts     int64 `json:"repair_attempts"`
}

// PolicyRef stamps {policy_id, policy_version} per invariant I-09.
type PolicyRef struct {
	PolicyID string `json:"policy_id"`
	Version  int    `json:"policy_version"`
}

// IntentOrigin enumerates how an intent entered the system.
type IntentOrigin string

// Intent origins.
const (
	OriginAgentAPI   IntentOrigin = "agent_api"
	OriginCLI        IntentOrigin = "cli"
	OriginGitHubHook IntentOrigin = "github_webhook"
	OriginSynthetic  IntentOrigin = "synthetic"
)

// IntentDeclared carries the immutable declaration fields of an intent.
type IntentDeclared struct {
	Goal               string       `json:"goal"`
	Repo               string       `json:"repo"`
	BaseRef            string       `json:"base_ref"`
	BaseSnapshot       string       `json:"base_snapshot"`
	OwnedSurfaces      []string     `json:"owned_surfaces"`
	Constraints        []string     `json:"constraints"`
	AcceptanceCriteria []string     `json:"acceptance_criteria"`
	RiskClass          RiskClass    `json:"risk_class"`
	Deadline           *time.Time   `json:"deadline,omitempty"`
	Origin             IntentOrigin `json:"origin"`
	AgentLineage       []string     `json:"agent_lineage"`
	ResolvedPolicy     PolicyRef    `json:"resolved_policy"`
	ComputeBudget      BudgetValues `json:"compute_budget"`
}

// ConflictRef surfaces an admission-time overlap with another active intent.
type ConflictRef struct {
	IntentID       string `json:"intent_id"`
	Relation       string `json:"relation"`
	Owner          string `json:"owner"`
	Recommendation string `json:"recommendation"`
}

// Intent is the declared change an agent or human wants validated.
type Intent struct {
	ID        string
	TenantID  string
	State     IntentState
	Declared  IntentDeclared
	CreatedAt time.Time
	ClosedAt  *time.Time
}

// NewIntent constructs an intent in the exploring state.
func NewIntent(id, tenantID string, d IntentDeclared, now time.Time) *Intent {
	return &Intent{ID: id, TenantID: tenantID, State: IntentExploring, Declared: d, CreatedAt: now}
}

type transitionRule struct {
	from []string
	to   string
}

func matchesState(from []string, cur string) bool {
	for _, f := range from {
		if f == cur {
			return true
		}
	}
	return false
}

var intentTransitions = map[string]transitionRule{
	"validation.planned":    {from: []string{string(IntentExploring)}, to: string(IntentValidating)},
	"failure.blocked":       {from: []string{string(IntentValidating)}, to: string(IntentBlocked)},
	"repair.authorized":     {from: []string{string(IntentValidating), string(IntentBlocked)}, to: string(IntentRepairing)},
	"candidate.resubmitted": {from: []string{string(IntentRepairing), string(IntentBlocked)}, to: string(IntentValidating)},
	"decision.eligible":     {from: []string{string(IntentValidating)}, to: string(IntentMergeReady)},
	"deploy.authorized":     {from: []string{string(IntentMergeReady)}, to: string(IntentDeploying)},
	"deploy.executed":       {from: []string{string(IntentDeploying)}, to: string(IntentMonitoring)},
	"post_merge.satisfied":  {from: []string{string(IntentMonitoring)}, to: string(IntentCompleted)},
	"intent.rejected":       {from: []string{string(IntentExploring), string(IntentValidating), string(IntentBlocked), string(IntentRepairing), string(IntentMergeReady), string(IntentDeploying), string(IntentMonitoring)}, to: string(IntentRejected)},
}

// Apply advances the intent's state machine on the named trigger.
// Terminal aggregates log-and-ignore every further event (I-08).
func (i *Intent) Apply(trigger string) error {
	if i.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := intentTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for intent", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(i.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, i.ID, i.State, trigger)
	}
	i.State = IntentState(rule.to)
	if i.State == IntentRejected {
		now := time.Now().UTC()
		i.ClosedAt = &now
	}
	if i.State == IntentCompleted {
		now := time.Now().UTC()
		i.ClosedAt = &now
	}
	return nil
}
