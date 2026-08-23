package planner

import (
	"sauron.dev/sauron/control-plane/internal/policy"
)

// Ladder composition helpers: which jobs each tier carries and how fallback
// triggers widen them. Kept apart from Plan's decision flow so the §3 table
// lives in one place.

// Non-fallback rationales per tier.
const (
	rationaleAdmission   = "admission_gate"
	rationaleImpact      = "impact_selection"
	rationaleContract    = "policy_contract_requirements"
	rationaleTier3Risk   = "policy_tier3_risk_class"
	rationaleComposition = "composition_validation"
	rationaleOverride    = "explicit_override"
)

func hasTrigger(triggers []string, want string) bool {
	for _, t := range triggers {
		if t == want {
			return true
		}
	}
	return false
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

func tierPlan(tier int, jobs []string, rationale string) TierPlan {
	return TierPlan{Tier: tier, Jobs: jobs, Rationale: rationale}
}

func specNames(jobs []JobSpec) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Name
	}
	return out
}

func joinFallbackRationale(triggers []string) string {
	out := ""
	for i, t := range triggers {
		if i > 0 {
			out += ","
		}
		out += "fallback:" + t
	}
	return out
}

func widenTier2(jobs []JobSpec) []JobSpec {
	out := make([]JobSpec, 0, len(jobs))
	for _, j := range jobs {
		if j.Name == "impacted_integration_tests" {
			out = append(out, tier2FullBase[0])
			continue
		}
		out = append(out, j)
	}
	return out
}

func composeTier2Jobs(required []string) []JobSpec {
	jobs := append([]JobSpec(nil), tier2SelectedBase...)
	for _, k := range required {
		switch k {
		case "payment_contract":
			jobs = append(jobs, tier2PaymentJob)
		case "idempotency_race":
			jobs = append(jobs, tier2IdempotencyJob)
		}
	}
	return jobs
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// explicitUncertaintyFallback reports whether trigger 1 or 7 fired — the §3
// gate note's "explicit uncertainty fallback" for tier-3 entry.
func explicitUncertaintyFallback(triggers []string) bool {
	for _, t := range triggers {
		if t == FallbackUncertainty || t == FallbackExplicitOverride {
			return true
		}
	}
	return false
}

// tier3Rationale explains why tier 3 is present: risk-gated entry gets the
// clean label; fallback/override entry composes its reasons.
func tier3Rationale(in CandidateInput, body policy.PolicyBody, triggers []string) string {
	if containsExact(body.LadderOverrides.Tier3RiskClasses, in.RiskClass) {
		return rationaleTier3Risk
	}
	var parts []string
	if in.ExplicitFullSuiteOverride {
		parts = append(parts, rationaleOverride)
	}
	if hasTrigger(triggers, FallbackUncertainty) {
		parts = append(parts, joinFallbackRationale(triggers))
	}
	return joinComma(parts)
}

// tier4Rationale explains tier-4 presence: composition (EC-017) or override,
// plus the fired fallback triggers.
func tier4Rationale(in CandidateInput, triggers []string) string {
	switch {
	case in.ComposingIntegrationSet && len(triggers) > 0:
		return joinComma([]string{rationaleComposition, joinFallbackRationale(triggers)})
	case in.ComposingIntegrationSet:
		return rationaleComposition
	default:
		return rationaleOverride
	}
}
