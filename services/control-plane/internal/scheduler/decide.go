package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/cluster"
	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// renderRequest carries everything a rendered decision needs (I-09 stamp
// mandatory).
type renderRequest struct {
	TenantID     string
	CandidateID  string
	PlanID       string
	Verb         domain.DecisionVerb
	Policy       domain.PolicyRef
	Summary      string
	EvidenceRefs []string
	InputsHash   string
}

// renderDecision appends the decision.rendered event, persists the decisions
// projection, and drives the downstream candidate/plan state changes plus
// cluster supersede propagation.
func (e *EngineScheduler) renderDecision(ctx context.Context, tx pgx.Tx, req renderRequest) error {
	cand, err := e.store.GetCandidate(ctx, req.TenantID, req.CandidateID)
	if err != nil {
		return fmt.Errorf("load candidate for decision: %w", err)
	}
	decision := &domain.Decision{
		ID:           domain.NewID(domain.PrefixDecision),
		TenantID:     req.TenantID,
		SubjectType:  domain.SubjectCandidate,
		SubjectID:    req.CandidateID,
		Verb:         req.Verb,
		Confidence:   decisionConfidence(req.Verb),
		Policy:       req.Policy,
		Explanation:  explanationFor(req),
		EvidenceRefs: req.EvidenceRefs,
		InputsHash:   req.InputsHash,
		RenderedAt:   time.Now().UTC(),
	}
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	ev, err := domain.NewEvent(req.TenantID,
		domain.AggregateRef{Type: string(domain.AggDecision), ID: decision.ID},
		"decision.rendered", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"decision_id":   decision.ID,
			"subject":       map[string]any{"type": "candidate", "id": req.CandidateID},
			"verb":          string(req.Verb),
			"confidence":    decision.Confidence,
			"policy":        map[string]any{"policy_id": req.Policy.PolicyID, "policy_version": req.Policy.Version},
			"explanation":   decision.Explanation,
			"evidence_refs": toAnySlice(req.EvidenceRefs),
			"inputs_hash":   req.InputsHash,
		})
	if err != nil {
		return err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return err
	}
	if err := store.InsertDecisionTx(ctx, tx, decision, ev.Seq); err != nil {
		return err
	}

	intentID := cand.IntentID
	switch req.Verb {
	case domain.VerbEligibleForMergeTrain:
		if err := store.MarkPlanSatisfiedTx(ctx, tx, req.TenantID, req.PlanID, ev.Seq); err != nil {
			return err
		}
		if err := store.MarkCandidateStateTx(ctx, tx, req.TenantID, req.CandidateID,
			string(domain.CandEligible), []string{"submitted", "planned", "validating", "repairing"}, ev.Seq); err != nil {
			return err
		}
		if err := e.propagateSupersession(ctx, tx, req.TenantID, cand); err != nil {
			return err
		}
	case domain.VerbRejected:
		if err := store.MarkCandidateStateTx(ctx, tx, req.TenantID, req.CandidateID,
			string(domain.CandRejected), []string{"submitted", "planned", "validating", "repairing"}, ev.Seq); err != nil {
			return err
		}
	}
	_ = intentID
	return e.maybeRevokeLease(ctx, tx, req.TenantID, intentID)
}

// propagateSupersession applies the cluster relation table when a
// representative renders eligible: duplicates are dominated, alternative
// tournament losers stand down (§2).
func (e *EngineScheduler) propagateSupersession(ctx context.Context, tx pgx.Tx, tenantID string, winner *domain.Candidate) error {
	snap, ok, err := e.store.ClusterForCandidate(ctx, tenantID, winner.ID)
	if err != nil || !ok {
		return err
	}
	active := toEngineCluster(snap)
	for _, d := range cluster.SupersedeDecisions(active, cluster.EventEligible) {
		memberCand, err := e.store.GetCandidate(ctx, tenantID, d.CandidateID)
		if err != nil {
			continue // member vanished; leave for reconciler
		}
		supersededEvent, err := newCandidateSupersededEvent(tenantID, d.CandidateID, winner.ID, active.Rep.ID)
		if err != nil {
			return err
		}
		if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{supersededEvent}); err != nil {
			return err
		}
		if err := store.MarkCandidateStateTx(ctx, tx, tenantID, d.CandidateID,
			"superseded", []string{"submitted", "planned", "validating", "blocked_representative", "repairing"}, supersededEvent.Seq); err != nil {
			return err
		}
		cancelled, err := store.CancelRunsForCandidateTx(ctx, tx, e.store, tenantID, d.CandidateID, "superseded")
		if err != nil {
			return err
		}
		for _, runID := range cancelled {
			if err := e.fleet.Cancel(ctx, runID, "superseded"); err != nil {
				logf("fleet cancel %s: %v", runID, err)
			}
		}
		_ = memberCand
	}
	return nil
}

// maybeRevokeLease revokes the intent's change-scope lease once no live
// candidates remain (intent solved / fully terminal).
func (e *EngineScheduler) maybeRevokeLease(ctx context.Context, tx pgx.Tx, tenantID, intentID string) error {
	live, err := store.LiveCandidateCountByIntentTx(ctx, tx, tenantID, intentID)
	if err != nil || live > 0 {
		return err
	}
	leases, err := e.store.LeaseForIntent(ctx, tenantID, intentID)
	if err != nil || len(leases) == 0 {
		return err
	}
	lease, err := e.store.GetLease(ctx, tenantID, leases[0].ID)
	if err != nil {
		return err
	}
	if err := lease.Apply("lease.revoked"); err != nil {
		return nil // already released/expired; idempotent no-op
	}
	return e.store.ReleaseLeaseTx(ctx, tx, lease, "superseded")
}

func newCandidateSupersededEvent(tenantID, candidateID, byCandidateID, repID string) (*domain.Event, error) {
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	return domain.NewEvent(tenantID,
		domain.AggregateRef{Type: string(domain.AggCandidate), ID: candidateID},
		"candidate.superseded", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"candidate_id":    candidateID,
			"by_candidate_id": byCandidateID,
			"relation":        "duplicate_of",
			"reason":          "dominated_duplicate",
		})
}

func toEngineCluster(snap *store.ClusterSnapshot) cluster.ActiveCluster {
	active := cluster.ActiveCluster{ID: snap.ID, RepoID: snap.Repo}
	for i, m := range snap.Members {
		if m.Member.ID == snap.RepCandidateID && i == 0 {
			active.Rep = m.Member
			continue
		}
		active.Members = append(active.Members, m)
	}
	return active
}

func explanationFor(req renderRequest) domain.Explanation {
	return domain.Explanation{
		Summary: req.Summary,
		Factors: []domain.ExplanationFactor{
			{Name: "policy_id", Value: req.Policy.PolicyID, Source: "resolved_policy"},
			{Name: "policy_version", Value: req.Policy.Version, Source: "resolved_policy"},
		},
	}
}

func decisionConfidence(verb domain.DecisionVerb) float64 {
	if verb == domain.VerbDeferred {
		return 0.6
	}
	return 0.95
}
