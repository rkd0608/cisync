// Package policy resolves the active policy document governing a change
// subject (DOMAIN_MODEL_DRAFT §8). Resolution is fail-closed per I-09: with
// no resolvable active policy, Resolve returns an error and callers must not
// plan, admit, authorize or render decisions. The v1 policy source is the
// compiled-in default pack (default_pack.go); store-backed registries adapt
// to the Registry interface defined here.
package policy

import (
	"errors"
	"fmt"
	"sort"
)

// Policy lifecycle statuses (DOMAIN_MODEL_DRAFT §1.10).
const (
	StatusDraft   = "draft"
	StatusActive  = "active"
	StatusRetired = "retired"
)

// Sentinel resolution errors.
var (
	// ErrNoActivePolicy is returned when no active policy version matches the
	// subject; it always blocks the gated operation (I-09 fail-closed).
	ErrNoActivePolicy = errors.New("policy: no resolvable active policy")
	// ErrRegistryFailed wraps a registry lookup failure; resolution also
	// fails closed in this case.
	ErrRegistryFailed = errors.New("policy: registry lookup failed")
)

// ActorSelectors narrows applicability by acting agent identity. Agents holds
// glob patterns such as "agent:*" (empty means any actor); Exclude removes
// actors even when matched by Agents.
type ActorSelectors struct {
	Agents  []string `json:"agents"`
	Exclude []string `json:"exclude"`
}

// ScopeSelectors identifies the population a policy version applies to. An
// empty dimension is a wildcard for that dimension.
type ScopeSelectors struct {
	Repos       []string       `json:"repos"`
	Paths       []string       `json:"paths"`
	RiskClasses []string       `json:"risk_classes"`
	Actors      ActorSelectors `json:"actors"`
}

// Subject is the change-side scope a policy is resolved against.
type Subject struct {
	Repo         string
	ChangedPaths []string
	RiskClass    string
	Actor        string
}

// PerCandidateBudget is budgets.per_candidate from §8.
type PerCandidateBudget struct {
	CPUMinutes         int64 `json:"cpu_minutes"`
	EnvironmentMinutes int64 `json:"environment_minutes"`
	RepairAttempts     int64 `json:"repair_attempts"`
}

// PerTenantHourBudget is budgets.per_tenant_hour from §8.
type PerTenantHourBudget struct {
	CPUMinutes           int64 `json:"cpu_minutes"`
	ConcurrentCandidates int64 `json:"concurrent_candidates"`
}

// EnvTemplateCap is one budgets.env_templates entry.
type EnvTemplateCap struct {
	MaxConcurrent int64 `json:"max_concurrent"`
}

// Budgets mirrors the §8 budgets object. WIPByTier keys are tier numbers as
// text ("0".."4") because they arrive as JSON object keys.
type Budgets struct {
	PerCandidate  PerCandidateBudget        `json:"per_candidate"`
	PerTenantHour PerTenantHourBudget       `json:"per_tenant_hour"`
	WIPByTier     map[string]int            `json:"wip_by_tier"`
	EnvTemplates  map[string]EnvTemplateCap `json:"env_templates"`
	ValueTiers    map[string]float64        `json:"value_tiers"`
}

// LadderOverrides mirrors the §8 ladder_overrides object.
type LadderOverrides struct {
	Tier3RiskClasses       []string `json:"tier3_risk_classes"`
	MinSelectionConfidence float64  `json:"min_selection_confidence"`
	FallbackTriggers       []string `json:"fallback_triggers"`
	ProtectedPaths         []string `json:"protected_paths"`
}

// Autonomy mirrors the §8 autonomy object.
type Autonomy struct {
	Level                int               `json:"level"`
	LevelsSemantics      map[string]string `json:"levels_semantics"`
	AutoMergeRiskClasses []string          `json:"auto_merge_risk_classes"`
	AutoRepairClasses    []string          `json:"auto_repair_classes"`
	EscalateOn           []string          `json:"escalate_on"`
}

// PolicyBody is the full §8 policy document shape.
type PolicyBody struct {
	PolicyID               string              `json:"policy_id"`
	Version                int                 `json:"version"`
	ScopeSelectors         ScopeSelectors      `json:"scope_selectors"`
	RequiredEvidenceByRisk map[string][]string `json:"required_evidence_by_risk"`
	LadderOverrides        LadderOverrides     `json:"ladder_overrides"`
	Budgets                Budgets             `json:"budgets"`
	Autonomy               Autonomy            `json:"autonomy"`
}

// PolicyRecord is a versioned policy aggregate (§1.10) as seen by the resolver.
type PolicyRecord struct {
	ID      string     // pol_ ULID or family id such as "pol_payments_high_risk"
	Version int        // monotonic within the family
	Status  string     // draft|active|retired
	Body    PolicyBody // immutable once active
}

// ResolvedPolicy stamps {policy_id, policy_version} (I-09) and carries the
// winning body. Field names match domain.PolicyRef for mechanical adaptation.
type ResolvedPolicy struct {
	PolicyID string
	Version  int
	Body     PolicyBody
}

// Registry supplies candidate policies for resolution. Store adapters
// implement it; DefaultRegistry serves the compiled-in pack.
type Registry interface {
	ActivePolicies() ([]PolicyRecord, error)
}

// RegistryFunc adapts an ordinary function to Registry.
type RegistryFunc func() ([]PolicyRecord, error)

// ActivePolicies implements Registry.
func (f RegistryFunc) ActivePolicies() ([]PolicyRecord, error) { return f() }

// Specificity ranks selector dimensions per §8: paths > repos > wildcard.
const (
	specificityWildcard = 0
	specificityRepos    = 2
	specificityPaths    = 4
)

// Resolve picks the most-specific active policy matching subject:
// specificity (paths > repos > wildcard), then highest version, then
// lexicographically greatest ID for total determinism. With zero eligible
// records it fails closed with ErrNoActivePolicy (I-09).
func Resolve(subject Subject, reg Registry) (ResolvedPolicy, error) {
	if reg == nil {
		return ResolvedPolicy{}, fmt.Errorf("%w: nil registry for repo %q risk %q", ErrNoActivePolicy, subject.Repo, subject.RiskClass)
	}
	recs, err := reg.ActivePolicies()
	if err != nil {
		return ResolvedPolicy{}, fmt.Errorf("%w: %v", ErrRegistryFailed, err)
	}
	best := -1
	bestSpec := -1
	for i, rec := range recs {
		if rec.Status != StatusActive || rec.Body.PolicyID == "" || rec.Version <= 0 {
			continue
		}
		spec, ok := matchSpecificity(rec.Body.ScopeSelectors, subject)
		if !ok {
			continue
		}
		if better(spec, rec, bestSpec, best, recs) {
			best, bestSpec = i, spec
		}
	}
	if best < 0 {
		return ResolvedPolicy{}, fmt.Errorf("%w: repo %q risk %q actor %q", ErrNoActivePolicy, subject.Repo, subject.RiskClass, subject.Actor)
	}
	win := recs[best]
	return ResolvedPolicy{PolicyID: win.ID, Version: win.Version, Body: win.Body}, nil
}

func better(spec int, rec PolicyRecord, bestSpec int, best int, recs []PolicyRecord) bool {
	if best < 0 {
		return true
	}
	if spec != bestSpec {
		return spec > bestSpec
	}
	if rec.Version != recs[best].Version {
		return rec.Version > recs[best].Version
	}
	return rec.ID > recs[best].ID
}

// matchSpecificity reports whether all present selector dimensions match the
// subject and, if so, the specificity rank of the match.
func matchSpecificity(sel ScopeSelectors, s Subject) (int, bool) {
	if len(sel.Repos) > 0 && !matchAnyGlob(sel.Repos, s.Repo) {
		return specificityWildcard, false
	}
	if len(sel.Paths) > 0 {
		matched := false
		for _, p := range s.ChangedPaths {
			if matchAnyGlob(sel.Paths, p) {
				matched = true
				break
			}
		}
		if !matched {
			return specificityWildcard, false
		}
	}
	if len(sel.RiskClasses) > 0 && !containsExact(sel.RiskClasses, s.RiskClass) {
		return specificityWildcard, false
	}
	if len(sel.Actors.Agents) > 0 && !matchAnyGlob(sel.Actors.Agents, s.Actor) {
		return specificityWildcard, false
	}
	if len(sel.Actors.Exclude) > 0 && matchAnyGlob(sel.Actors.Exclude, s.Actor) {
		return specificityWildcard, false
	}
	switch {
	case len(sel.Paths) > 0:
		return specificityPaths, true
	case len(sel.Repos) > 0:
		return specificityRepos, true
	default:
		return specificityWildcard, true
	}
}

func matchAnyGlob(patterns []string, value string) bool {
	for _, p := range patterns {
		if MatchGlob(p, value) {
			return true
		}
	}
	return false
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// SortRecords orders policy records deterministically (id, then version) so
// registry adapters can persist or log them reproducibly.
func SortRecords(recs []PolicyRecord) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].ID != recs[j].ID {
			return recs[i].ID < recs[j].ID
		}
		return recs[i].Version < recs[j].Version
	})
}
