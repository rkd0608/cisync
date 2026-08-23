package planner

import (
	"context"
	"fmt"
	"time"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/policy"
)

// EnginePlanner adapts the pure selection planner (Plan + fallback triggers)
// to the domain.Planner port. Policy resolution is fail-closed per I-09: an
// unresolvable active policy aborts planning.
type EnginePlanner struct {
	registry policy.Registry
}

// NewEnginePlanner wires the adapter over a policy registry. The compiled-in
// default registry is used when reg is nil.
func NewEnginePlanner(reg policy.Registry) *EnginePlanner {
	if reg == nil {
		reg = policy.DefaultRegistry()
	}
	return &EnginePlanner{registry: reg}
}

// Plan implements domain.Planner: resolve the active policy for the change,
// run the deterministic ladder composition, and stamp identity/timestamps.
func (p *EnginePlanner) Plan(_ context.Context, cand domain.CandidateInput, _ domain.ResolvedPolicy) (*domain.ValidationPlan, error) {
	resolved, err := policy.Resolve(policy.Subject{
		Repo:         cand.Repo,
		ChangedPaths: cand.ChangedPaths,
		RiskClass:    string(cand.RiskClass),
	}, p.registry)
	if err != nil {
		return nil, err // already wrapped as ErrNoActivePolicy / ErrRegistryFailed (I-09 fail closed)
	}
	out, err := Plan(engineInput(cand), resolved)
	if err != nil {
		return nil, fmt.Errorf("planner engine: %w", err)
	}
	tiers := make([]domain.Tier, 0, len(out.Tiers))
	for _, t := range out.Tiers {
		tiers = append(tiers, domain.Tier{
			Tier:                t.Tier,
			Jobs:                append([]string(nil), t.Jobs...),
			Rationale:           t.Rationale,
			SelectionConfidence: t.SelectionConfidence,
		})
	}
	return domain.NewValidationPlan(
		domain.NewID(domain.PrefixPlan),
		"", // tenant stamped by the API layer after planning
		cand.CandidateID,
		tiers,
		append([]string(nil), out.RequiredEvidenceKinds...),
		domain.PolicyRef{PolicyID: out.PolicyRef.PolicyID, Version: out.PolicyRef.Version},
		out.InputsHash,
		time.Now().UTC(),
	), nil
}

// engineInput maps the port shape onto the engine input. Learned-stats
// telemetry fields are left zero for v1: no history exists yet, so the §3
// sparse-data fallbacks fire by design (full-suite selection, recorded in
// plan rationales).
func engineInput(cand domain.CandidateInput) CandidateInput {
	return CandidateInput{
		CandidateID:  cand.CandidateID,
		IntentID:     cand.IntentID,
		Repo:         cand.Repo,
		BaseSHA:      cand.BaseSHA,
		HeadSHA:      cand.HeadSHA,
		PatchRef:     cand.PatchRef,
		RiskClass:    string(cand.RiskClass),
		ChangedPaths: cand.ChangedPaths,
	}
}

var _ domain.Planner = (*EnginePlanner)(nil)
