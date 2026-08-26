package failure

import (
	"cisync.dev/cisync/control-plane/internal/policy"
)

// MaxRepairIterations is the v0 hard bound on repair loop iterations
// (EC-021: repair budget exhaustion closes deterministically).
const MaxRepairIterations = 2

// Minimum autonomy level for the repairing transition (§1.1 gate: P, ≥3).
const MinRepairAutonomyLevel = 3

// RepairContext carries the intent-side contract an authorized patch must
// stay inside (I-05).
type RepairContext struct {
	IntentOwnedSurfaces []string // glob patterns granted to the intent
	ProhibitedPaths     []string // glob patterns always excluded
	RiskClass           string
	FailedAssertion     string
}

// RepairEnvelope is the spec's repair envelope (§1.9).
type RepairEnvelope struct {
	ReproductionCommand         string
	FailedAssertion             string
	SuspectedDiffHunks          []string
	AllowedPaths                []string
	ProhibitedPaths             []string
	MaxIterations               int64
	RequiredEvidenceAfterRepair []string
}

// RepairAuthorization is the ruling plus the envelope when authorized.
type RepairAuthorization struct {
	Authorized bool
	DenyReason string
	Envelope   RepairEnvelope
}

// Denial reasons in evaluation order.
const (
	DenySecurityViolation  = "security_violation_never_auto_repaired"
	DenyAutonomy           = "autonomy_below_minimum"
	DenyClassNotRepairable = "class_not_repairable"
	DenyNoAllowedSurface   = "no_allowed_surface"
	DenyUnknownRiskClass   = "unknown_risk_class"
)

// AuthorizeRepair decides whether a classified failure may enter a bounded
// repair loop and produces its envelope.
//
// Gates, in order: security/policy violations are never auto-repaired;
// autonomy level must be ≥3; the class must be policy-repairable; at least
// one intent-owned surface must survive prohibited-path filtering. The
// envelope confines patches to allowed_paths ⊆ intent owned surfaces minus
// prohibited paths, preserves the prohibited list verbatim, and caps
// iterations at MaxRepairIterations regardless of a larger policy grant.
func AuthorizeRepair(fc FailureCase, rp policy.ResolvedPolicy, rc RepairContext) RepairAuthorization {
	if fc.Classification == ClassSecurityPolicyViolation {
		return RepairAuthorization{DenyReason: DenySecurityViolation}
	}
	if rp.Body.Autonomy.Level < MinRepairAutonomyLevel {
		return RepairAuthorization{DenyReason: DenyAutonomy}
	}
	if !containsExact(rp.Body.Autonomy.AutoRepairClasses, fc.Classification) {
		return RepairAuthorization{DenyReason: DenyClassNotRepairable}
	}
	required, ok := rp.Body.RequiredEvidenceByRisk[rc.RiskClass]
	if !ok {
		return RepairAuthorization{DenyReason: DenyUnknownRiskClass}
	}
	allowed := filterProhibited(rc.IntentOwnedSurfaces, rc.ProhibitedPaths)
	if len(allowed) == 0 {
		return RepairAuthorization{DenyReason: DenyNoAllowedSurface}
	}
	maxIter := rp.Body.Budgets.PerCandidate.RepairAttempts
	if maxIter <= 0 || maxIter > MaxRepairIterations {
		maxIter = MaxRepairIterations
	}
	hunks := append([]string(nil), fc.SuspectedPaths...)
	return RepairAuthorization{
		Authorized: true,
		Envelope: RepairEnvelope{
			ReproductionCommand:         fc.ReproductionCommand,
			FailedAssertion:             rc.FailedAssertion,
			SuspectedDiffHunks:          hunks,
			AllowedPaths:                allowed,
			ProhibitedPaths:             append([]string(nil), rc.ProhibitedPaths...),
			MaxIterations:               maxIter,
			RequiredEvidenceAfterRepair: append([]string(nil), required...),
		},
	}
}

func filterProhibited(surfaces, prohibited []string) []string {
	var out []string
	for _, s := range surfaces {
		if !matchAnyGlob(prohibited, s) {
			out = append(out, s)
		}
	}
	sortStrings(out)
	return out
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func matchAnyGlob(patterns []string, value string) bool {
	for _, p := range patterns {
		if policy.MatchGlob(p, value) {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
