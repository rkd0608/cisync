package api

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	plannerengine "cisync.dev/cisync/control-plane/internal/planner"
	"cisync.dev/cisync/control-plane/internal/store"
)

// createSyntheticIntentTx mints the D12 synthetic intent + change-scope
// lease for a PR that arrived without a declared intent. Origin is stamped
// github_webhook so the UI can flag platform-created intents.
func (s *Server) createSyntheticIntentTx(ctx context.Context, tx pgx.Tx, tenant string, view deliveryView) (*domain.Intent, error) {
	pol := domain.DefaultPolicy()
	now := nowUTC()
	intent := domain.NewIntent(domain.NewID(domain.PrefixIntent), tenant, domain.IntentDeclared{
		Goal:               fmt.Sprintf("synthetic: %s (#%d)", view.PR.Title, view.PR.Number),
		Repo:               view.Repo,
		BaseRef:            view.PR.BaseRef,
		BaseSnapshot:       view.PR.BaseSHA,
		OwnedSurfaces:      []string{"**"},
		Constraints:        []string{},
		AcceptanceCriteria: []string{},
		RiskClass:          domain.RiskMedium,
		Origin:             domain.OriginGitHubHook,
		AgentLineage:       []string{"github:" + view.PR.Sender},
		ResolvedPolicy:     pol.Ref,
		ComputeBudget:      pol.PerCandidateBudget,
	}, now)
	intent.PRNumber = view.PR.Number
	lease := domain.NewLease(domain.NewID(domain.PrefixLease), tenant, intent.ID,
		domain.LeaseScope{Kind: domain.ScopeChangeScope, Surfaces: []string{"**"}},
		webhookActor(view.PR.Sender).ID, pol.PerCandidateBudget, s.cfg.DefaultLeaseTTL,
		[]string{}, now)
	if _, err := store.CreateIntentTx(ctx, tx, s.store, intent, lease, nil); err != nil {
		return nil, err
	}
	s.metrics.Add("cisync_ctrl_webhook_synthetic_intents_total", 1)
	return intent, nil
}

// submitWebhookCandidateTx registers one PR-head candidate with a fresh plan
// (fresh inputs_hash per I-02) — shared by pr.opened and pr.synchronize.
func (s *Server) submitWebhookCandidateTx(ctx context.Context, tx pgx.Tx, intent *domain.Intent, tenant string, view deliveryView) (*domain.Candidate, error) {
	liveHead, knownHead, err := store.CandidateHeadStateTx(ctx, tx, tenant, intent.ID, view.PR.HeadSHA)
	if err != nil {
		return nil, err
	}
	if knownHead && liveHead {
		s.metrics.Add("cisync_ctrl_webhook_replays_total", 1)
		return nil, nil // duplicate head already live ⇒ idempotent replay
	}
	pol := domain.DefaultPolicy()
	now := nowUTC()
	cand, err := domain.NewCandidate(domain.NewID(domain.PrefixCandidate), tenant, intent.ID,
		"github:"+view.PR.Sender, view.PR.DiffURL, view.PR.HeadSHA, view.PR.BaseSHA,
		[]string{}, 0, now)
	if err != nil {
		return nil, err
	}
	cand.Repo = view.Repo
	cand.PRNumber = view.PR.Number
	plan, err := s.planner.Plan(ctx, domain.CandidateInput{
		CandidateID: cand.ID,
		IntentID:    intent.ID,
		Repo:        view.Repo,
		BaseSHA:     view.PR.BaseSHA,
		HeadSHA:     view.PR.HeadSHA,
		PatchRef:    view.PR.DiffURL,
		RiskClass:   intent.Declared.RiskClass,
	}, pol)
	if err != nil {
		return nil, err
	}
	plan.TenantID = tenant
	base := riskPriority[intent.Declared.RiskClass]
	var runs []*domain.ValidationRun
	for _, tier := range plan.Tiers {
		def := tierDefaults[tier.Tier]
		for _, jobName := range tier.Jobs {
			spec := jobSpecFor(intent, cand, plan)
			spec.Kind = plannerengine.EvidenceKindForJob(jobName)
			runs = append(runs, domain.NewValidationRun(domain.NewID(domain.PrefixRun),
				tenant, plan.ID, cand.ID, tier.Tier, spec, "sim",
				def.durationMS, def.costMC, base*float64(10-tier.Tier)/10, now))
			cand.EstCostMillicents += def.costMC
		}
	}
	events, err := store.SubmitCandidateTx(ctx, tx, s.store, cand, plan, runs, nil)
	if err != nil {
		return nil, err
	}
	s.metrics.Add("cisync_ctrl_events_appended_total", float64(len(events)))
	s.metrics.Add("cisync_ctrl_webhook_candidates_submitted_total", 1)
	return cand, nil
}
