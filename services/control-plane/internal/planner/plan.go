package planner

import (
	"errors"
	"fmt"

	"sauron.dev/sauron/control-plane/internal/policy"
)

// ErrInvalidInput reports a candidate input the planner refuses to plan for;
// callers must fail closed rather than guess a ladder.
var ErrInvalidInput = errors.New("planner: invalid candidate input")

// CandidateInput is everything planning needs about one candidate. Field
// names mirror domain.CandidateInput where they overlap; telemetry fields
// feed the §3 fallback triggers.
type CandidateInput struct {
	CandidateID  string
	IntentID     string
	TenantID     string
	Repo         string
	BaseSHA      string
	HeadSHA      string
	PatchRef     string
	RiskClass    string
	ChangedPaths []string

	Lockfiles []string
	Flags     []string
	Toolchain string

	// SelectionConfidence is the impact model's confidence for the selected
	// suites; nil means no learned-stats history (confidence defaults to
	// NoHistorySelectionConfidence).
	SelectionConfidence *float64
	// SurfaceSamples maps surface class → observation count in the
	// learned-stats table (stats.test_outcomes).
	SurfaceSamples map[string]int
	// QuarantinedOrFlakeSignals lists members of the selected set with an
	// active flake signal or quarantine within the last 14 days (trigger 4).
	QuarantinedOrFlakeSignals []string

	ComposingIntegrationSet bool // integration-set assembly (trigger 5)
	RelationConflicting     bool // candidate relation is conflicts_with (trigger 5)

	// AmbiguousRetryFailureConfidence, when set, is the failure class
	// confidence of the retried run that led to this replan (trigger 6).
	AmbiguousRetryFailureConfidence *float64

	ExplicitFullSuiteOverride bool // trigger 7: policy override / human request
}

// TierPlan mirrors domain.Tier field names; Jobs are job names resolvable via
// JobCatalog for adapters needing full specs.
type TierPlan struct {
	Tier                int      `json:"tier"`
	Jobs                []string `json:"jobs"`
	Rationale           string   `json:"rationale"`
	SelectionConfidence *float64 `json:"selection_confidence,omitempty"`
}

// ValidationPlan mirrors domain.ValidationPlan field names minus identity and
// timestamps, which the integrator stamps (plans stay deterministic).
type ValidationPlan struct {
	CandidateID           string      `json:"candidate_id"`
	PolicyRef             PolicyStamp `json:"policy"`
	Tiers                 []TierPlan  `json:"tiers"`
	RequiredEvidenceKinds []string    `json:"required_evidence_kinds"`
	InputsHash            string      `json:"inputs_hash"`
	FallbackTriggers      []string    `json:"fallback_triggers,omitempty"`
}

// PolicyStamp is {policy_id, policy_version} per I-09.
type PolicyStamp struct {
	PolicyID string `json:"policy_id"`
	Version  int    `json:"policy_version"`
}

// Plan builds the deterministic validation plan for one candidate against a
// resolved policy.
//
// Ladder composition: tiers 0–2 are always planned (§3 auto-promotion chain).
// Tier 3 requires risk_class ∈ policy.tier3_risk_classes or an explicit
// uncertainty fallback (triggers 1/7). Tier 4 is planned when composing an
// integration set (EC-017) or on explicit override — merge-track promotion at
// runtime remains P-gated.
//
// Fallback semantics: any fired trigger widens the selective suites of tiers
// 1–2 to their full-suite variants and records "fallback:<trigger>"
// rationales (comma-joined when multiple fire, in §3 order) so decisions stay
// explainable (I-09).
func Plan(in CandidateInput, rp policy.ResolvedPolicy) (ValidationPlan, error) {
	if err := validateInput(in); err != nil {
		return ValidationPlan{}, err
	}
	if rp.PolicyID == "" || rp.Version <= 0 {
		return ValidationPlan{}, fmt.Errorf("%w: unresolved policy stamp", ErrInvalidInput)
	}
	body := rp.Body
	required, ok := body.RequiredEvidenceByRisk[in.RiskClass]
	if !ok || len(required) == 0 {
		return ValidationPlan{}, fmt.Errorf("%w: no required evidence mapping for risk class %q", ErrInvalidInput, in.RiskClass)
	}

	triggers := evaluateFallbacks(in, body)
	widened := len(triggers) > 0
	fallbackRationale := joinFallbackRationale(triggers)
	conf := effectiveSelectionConfidence(in)
	confCopy := conf

	tiers := make([]TierPlan, 0, 5)

	tiers = append(tiers, tierPlan(TierAdmission, specNames(tier0Jobs), rationaleAdmission))

	t1Jobs := tier1SelectedJobs
	t1Rationale := rationaleImpact
	if widened {
		t1Jobs = tier1FullJobs
		t1Rationale = fallbackRationale
	}
	tp1 := tierPlan(TierLocal, specNames(t1Jobs), t1Rationale)
	tp1.SelectionConfidence = &confCopy
	tiers = append(tiers, tp1)

	t2Jobs := composeTier2Jobs(required)
	t2Rationale := rationaleContract
	if widened {
		t2Jobs = widenTier2(t2Jobs)
		t2Rationale = fallbackRationale
	}
	tp2 := tierPlan(TierContract, specNames(t2Jobs), t2Rationale)
	tp2.SelectionConfidence = &confCopy
	tiers = append(tiers, tp2)

	if containsExact(body.LadderOverrides.Tier3RiskClasses, in.RiskClass) || explicitUncertaintyFallback(triggers) {
		tiers = append(tiers, tierPlan(TierSystem, specNames(tier3Jobs), tier3Rationale(in, body, triggers)))
	}

	if in.ComposingIntegrationSet || in.ExplicitFullSuiteOverride {
		tiers = append(tiers, tierPlan(TierIntegration, specNames(tier4Jobs), tier4Rationale(in, triggers)))
	}

	return ValidationPlan{
		CandidateID:           in.CandidateID,
		PolicyRef:             PolicyStamp{PolicyID: rp.PolicyID, Version: rp.Version},
		Tiers:                 tiers,
		RequiredEvidenceKinds: append([]string(nil), required...),
		InputsHash:            HashInputs(InputsMaterial{BaseSHA: in.BaseSHA, Lockfiles: in.Lockfiles, Flags: in.Flags, Toolchain: in.Toolchain}),
		FallbackTriggers:      triggers,
	}, nil
}

func validateInput(in CandidateInput) error {
	if in.CandidateID == "" {
		return fmt.Errorf("%w: empty candidate_id", ErrInvalidInput)
	}
	if in.BaseSHA == "" || in.HeadSHA == "" || in.BaseSHA == in.HeadSHA {
		return fmt.Errorf("%w: head_sha must differ from base_sha", ErrInvalidInput)
	}
	return nil
}
