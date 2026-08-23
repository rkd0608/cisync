// Package testsupport provides deterministic test doubles for the domain
// ports. Nothing here may be wired in production main paths.
package testsupport

import (
	"context"
	"fmt"
	"time"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// StaticPlanner is a deterministic two-tier Planner double: it always
// produces a Tier0+Tier1 plan per the resolved policy.
type StaticPlanner struct{}

// Plan implements domain.Planner.
func (StaticPlanner) Plan(_ context.Context, cand domain.CandidateInput, pol domain.ResolvedPolicy) (*domain.ValidationPlan, error) {
	if cand.CandidateID == "" || pol.Ref.PolicyID == "" {
		return nil, fmt.Errorf("%w: candidate id and resolved policy are required", domain.ErrValidationFailed)
	}
	requiredKinds, ok := pol.RequiredEvidence(cand.RiskClass)
	if !ok {
		return nil, fmt.Errorf("%w: no required evidence resolvable for risk %q", domain.ErrValidationFailed, cand.RiskClass)
	}
	tiers := []domain.Tier{
		{
			Tier:      0,
			Jobs:      []string{"secret_scan", "format_lint", "typecheck_lite", "diff_sanity", "policy_admissibility"},
			Rationale: "admission checks; all pass auto-promotes to tier 1",
		},
		{
			Tier:      1,
			Jobs:      []string{"compile_affected", "selected_unit", "sast_diff"},
			Rationale: "local impact per default policy",
		},
	}
	return domain.NewValidationPlan(
		domain.NewID(domain.PrefixPlan), "", cand.CandidateID, tiers, requiredKinds,
		pol.Ref, domain.InputsHash(cand.BaseSHA, cand.HeadSHA, cand.PatchRef), time.Now().UTC(),
	), nil
}

var _ domain.Planner = StaticPlanner{}
