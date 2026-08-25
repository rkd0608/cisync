package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// ClusterAssignmentData carries the clustering decision made at submission
// so the cluster.assigned event and projections commit atomically with the
// candidate (§2: assignment at candidate submission).
type ClusterAssignmentData struct {
	Joined            bool
	ClusterID         string
	Repo              string
	RepCandidateID    string
	RelationToRep     string
	PathOverlap       float64
	TrigramSimilarity float64
	SymbolOverlap     float64
	StrategyVersion   string
}

// SubmitCandidateTx persists candidate + plan + queued runs and appends
// candidate.submitted, validation.planned, validation.requested and — when
// clustered — cluster.assigned atomically.
func SubmitCandidateTx(ctx context.Context, tx pgx.Tx, s *Store, cand *domain.Candidate, plan *domain.ValidationPlan, runs []*domain.ValidationRun, assignment *ClusterAssignmentData) ([]*domain.Event, error) {
	corr := domain.NewCorrelationID()
	actor := domain.EventActor{Kind: string(domain.ActorAgent), ID: cand.Submitter}

	submittedEvent, err := domain.NewEvent(cand.TenantID,
		domain.AggregateRef{Type: string(domain.AggCandidate), ID: cand.ID},
		"candidate.submitted", "", corr, actor, map[string]any{
			"candidate_id":        cand.ID,
			"intent_id":           cand.IntentID,
			"submitter":           cand.Submitter,
			"patch_ref":           cand.PatchRef,
			"head_sha":            cand.HeadSHA,
			"base_sha":            cand.BaseSHA,
			"changed_paths":       toAnySlice(cand.ChangedPaths),
			"est_cost_millicents": cand.EstCostMillicents,
		})
	if err != nil {
		return nil, err
	}

	tiersAny := make([]any, 0, len(plan.Tiers))
	for _, t := range plan.Tiers {
		tier := map[string]any{"tier": t.Tier, "jobs": toAnySlice(t.Jobs), "rationale": t.Rationale}
		if t.SelectionConfidence != nil {
			tier["selection_confidence"] = *t.SelectionConfidence
		}
		tiersAny = append(tiersAny, tier)
	}
	plannedEvent, err := domain.NewEvent(cand.TenantID,
		domain.AggregateRef{Type: string(domain.AggPlan), ID: plan.ID},
		"validation.planned", submittedEvent.ID, corr, actor, map[string]any{
			"plan_id":                 plan.ID,
			"candidate_id":            plan.CandidateID,
			"tiers":                   tiersAny,
			"required_evidence_kinds": toAnySlice(plan.RequiredEvidenceKinds),
			"inputs_hash":             plan.InputsHash,
			"policy_version":          map[string]any{"policy_id": plan.Policy.PolicyID, "policy_version": plan.Policy.Version},
		})
	if err != nil {
		return nil, err
	}

	events := []*domain.Event{submittedEvent, plannedEvent}
	for _, run := range runs {
		requestedEvent, err := domain.NewEvent(cand.TenantID,
			domain.AggregateRef{Type: string(domain.AggRun), ID: run.ID},
			"validation.requested", plannedEvent.ID, corr, actor, map[string]any{
				"run_id":                  run.ID,
				"plan_id":                 plan.ID,
				"candidate_id":            run.CandidateID,
				"tier":                    run.Tier,
				"est_duration_ms":         run.EstDurationMS,
				"est_cost_millicents":     run.EstCostMillicents,
				"priority":                run.Priority,
				"cancellation_conditions": map[string]any{},
				"pool":                    run.Pool,
			})
		if err != nil {
			return nil, err
		}
		events = append(events, requestedEvent)
	}

	// WHY single batch: every AppendEventsTx takes the global chain lock, so
	// a second call here doubled lock hold time per submit (W3 storm finding).
	var assignedEvent *domain.Event
	if assignment != nil && (assignment.Joined || assignment.ClusterID != "") {
		ev, err := newClusterAssignedEvent(cand, assignment)
		if err != nil {
			return nil, err
		}
		assignedEvent = ev
		events = append(events, assignedEvent)
	}

	if err := s.AppendEventsTx(ctx, tx, events); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE ctrl.intents SET state='validating', seq=$3
		 WHERE id=$1 AND tenant_id=$2 AND state='exploring' AND seq < $3`,
		cand.IntentID, cand.TenantID, plannedEvent.Seq,
	); err != nil {
		return nil, fmt.Errorf("advance intent to validating: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.candidates (tenant_id, id, seq, intent_id, submitter, patch_ref, head_sha,
		   base_sha, changed_paths, est_cost_millicents, priority_score, cluster_id, relation_to_rep,
		   repo, pr_number, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state
		 WHERE ctrl.candidates.seq < EXCLUDED.seq`,
		cand.TenantID, cand.ID, submittedEvent.Seq, cand.IntentID, cand.Submitter, cand.PatchRef,
		cand.HeadSHA, cand.BaseSHA, toTextSlice(cand.ChangedPaths), cand.EstCostMillicents,
		cand.PriorityScore, nullableString(cand.ClusterID), relationOrNull(cand.RelationToRep),
		cand.Repo, cand.PRNumber, string(cand.State), cand.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert candidate projection: %w", err)
	}

	planTiersJSON, err := json.Marshal(tiersAny)
	if err != nil {
		return nil, fmt.Errorf("marshal tiers: %w", err)
	}
	kindsJSON, err := json.Marshal(toAnySlice(plan.RequiredEvidenceKinds))
	if err != nil {
		return nil, fmt.Errorf("marshal required kinds: %w", err)
	}
	policyJSON, err := json.Marshal(plan.Policy)
	if err != nil {
		return nil, fmt.Errorf("marshal plan policy: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.validation_plans (tenant_id, id, seq, candidate_id, policy, tiers,
		   required_evidence_kinds, inputs_hash, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state WHERE ctrl.validation_plans.seq < EXCLUDED.seq`,
		plan.TenantID, plan.ID, plannedEvent.Seq, plan.CandidateID, policyJSON, planTiersJSON,
		kindsJSON, plan.InputsHash, string(plan.State), plan.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert plan projection: %w", err)
	}

	for i, run := range runs {
		reqEvent := events[2+i]
		specJSON, err := json.Marshal(run.JobSpec)
		if err != nil {
			return nil, fmt.Errorf("marshal job spec: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.validation_runs (tenant_id, id, seq, plan_id, candidate_id, tier, job_spec,
			   attempt, pool, est_duration_ms, est_cost_millicents, priority, fence_token, timeout_ms,
			   state, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state
			 WHERE ctrl.validation_runs.seq < EXCLUDED.seq`,
			run.TenantID, run.ID, reqEvent.Seq, run.PlanID, run.CandidateID, run.Tier, specJSON,
			run.Attempt, run.Pool, run.EstDurationMS, run.EstCostMillicents, run.Priority,
			run.FenceToken, run.TimeoutMS, string(run.State), run.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("insert run projection: %w", err)
		}
	}

	if assignedEvent != nil {
		if err := s.applyClusterProjectionTx(ctx, tx, cand, assignment, assignedEvent); err != nil {
			return nil, err
		}
	}
	return events, nil
}

// newClusterAssignedEvent builds the cluster.assigned event so callers can
// batch it with the submit append (one global chain-lock acquisition).
func newClusterAssignedEvent(cand *domain.Candidate, a *ClusterAssignmentData) (*domain.Event, error) {
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "cluster"}
	return domain.NewEvent(cand.TenantID,
		domain.AggregateRef{Type: string(domain.AggCluster), ID: a.ClusterID},
		"cluster.assigned", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"cluster_id":       a.ClusterID,
			"candidate_id":     cand.ID,
			"rep_candidate_id": a.RepCandidateID,
			"relation":         a.RelationToRep,
			"similarity_score": a.TrigramSimilarity,
			"strategy_version": a.StrategyVersion,
		})
}

// applyClusterProjectionTx persists the cluster join (or new cluster)
// projection; its event was already appended in the submit batch.
func (s *Store) applyClusterProjectionTx(ctx context.Context, tx pgx.Tx, cand *domain.Candidate, a *ClusterAssignmentData, assignedEvent *domain.Event) error {

	// New cluster: this candidate found no qualifying representative and
	// seeds a singleton cluster it represents.
	if a.RepCandidateID == "" || a.RepCandidateID == cand.ID {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.clusters (tenant_id, id, seq, repo, rep_candidate_id, member_count, state, strategy_version, created_at)
			 VALUES ($1,$2,$3,$4,$5,1,'active',$6,now())`,
			cand.TenantID, a.ClusterID, assignedEvent.Seq, a.Repo, cand.ID, a.StrategyVersion); err != nil {
			return fmt.Errorf("insert cluster projection: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.cluster_members (cluster_id, candidate_id, relation_to_rep, similarity_score)
			 VALUES ($1,$2,$3,$4)`,
			a.ClusterID, cand.ID, a.RelationToRep, a.TrigramSimilarity); err != nil {
			return fmt.Errorf("insert cluster member: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ctrl.clusters SET member_count = member_count + 1 WHERE id=$1 AND tenant_id=$2`,
			a.ClusterID, cand.TenantID); err != nil {
			return fmt.Errorf("bump cluster membership: %w", err)
		}
	}
	return nil
}
