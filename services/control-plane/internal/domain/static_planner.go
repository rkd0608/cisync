package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// StaticPlanner is the built-in fallback Planner: it always produces a
// Tier0+Tier1 plan per the resolved policy. The real selection planner lands
// behind this same interface later.
type StaticPlanner struct{}

// Plan implements domain.Planner with a deterministic two-tier plan.
func (StaticPlanner) Plan(ctx context.Context, cand CandidateInput, pol ResolvedPolicy) (*ValidationPlan, error) {
	if cand.CandidateID == "" || pol.Ref.PolicyID == "" {
		return nil, fmt.Errorf("%w: candidate id and resolved policy are required", ErrValidationFailed)
	}
	requiredKinds, ok := pol.RequiredEvidence(cand.RiskClass)
	if !ok {
		return nil, fmt.Errorf("%w: no required evidence resolvable for risk %q", ErrValidationFailed, cand.RiskClass)
	}
	tiers := []Tier{
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
	return NewValidationPlan(
		NewID(PrefixPlan), "", cand.CandidateID, tiers, requiredKinds,
		pol.Ref, InputsHash(cand.BaseSHA, cand.HeadSHA, cand.PatchRef),
		time.Now().UTC(),
	), nil
}

// InputsHash derives the full evidence-reuse key over base SHA, head SHA and
// patch ref (invariant I-02).
func InputsHash(baseSHA, headSHA, patchRef string) string {
	h := sha256.Sum256([]byte(baseSHA + "\x00" + headSHA + "\x00" + patchRef))
	return HashPrefix + hex.EncodeToString(h[:])
}
