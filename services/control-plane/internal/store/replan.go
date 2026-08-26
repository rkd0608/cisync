package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// BuildValidationPlannedEvents renders the validation.planned +
// validation.requested events shared by fresh submissions (candidate_submit_tx)
// and replans (revalidate): one builder so payload shapes cannot drift.
// Returns [planned, requested*] — callers append via AppendEventsTx, which
// stamps the seqs used for projection writes.
func BuildValidationPlannedEvents(tenantID string, plan *domain.ValidationPlan, runs []*domain.ValidationRun, causationID, correlationID string, actor domain.EventActor) ([]*domain.Event, error) {
	tiersAny := make([]any, 0, len(plan.Tiers))
	for _, t := range plan.Tiers {
		tier := map[string]any{"tier": t.Tier, "jobs": toAnySlice(t.Jobs), "rationale": t.Rationale}
		if t.SelectionConfidence != nil {
			tier["selection_confidence"] = *t.SelectionConfidence
		}
		tiersAny = append(tiersAny, tier)
	}
	plannedEvent, err := domain.NewEvent(tenantID,
		domain.AggregateRef{Type: string(domain.AggPlan), ID: plan.ID},
		"validation.planned", causationID, correlationID, actor, map[string]any{
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
	events := []*domain.Event{plannedEvent}
	for _, run := range runs {
		requestedEvent, err := domain.NewEvent(tenantID,
			domain.AggregateRef{Type: string(domain.AggRun), ID: run.ID},
			"validation.requested", plannedEvent.ID, correlationID, actor, map[string]any{
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
	return events, nil
}

// AppendReplanTx persists a REPLAN: a new plan + queued runs for an EXISTING
// candidate (POST /candidates/{id}/revalidate), cancelling the candidate's
// prior queued runs so one revision never executes two plans at once.
func (s *Store) AppendReplanTx(ctx context.Context, tx pgx.Tx, plan *domain.ValidationPlan, runs []*domain.ValidationRun) ([]*domain.Event, error) {
	priorQueued, err := QueuedRunIDsTx(ctx, tx, plan.TenantID, plan.CandidateID)
	if err != nil {
		return nil, err
	}
	corr := domain.NewCorrelationID()
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "revalidate"}
	cancelledEvents := make([]*domain.Event, 0, len(priorQueued))
	for _, runID := range priorQueued {
		ev, err := domain.NewEvent(plan.TenantID,
			domain.AggregateRef{Type: string(domain.AggRun), ID: runID},
			"validation.cancelled", "", corr, actor,
			map[string]any{"run_ids": []any{runID}, "reason": "superseded"})
		if err != nil {
			return nil, err
		}
		cancelledEvents = append(cancelledEvents, ev)
	}
	plannedEvents, err := BuildValidationPlannedEvents(plan.TenantID, plan, runs, "", corr, actor)
	if err != nil {
		return nil, err
	}
	events := append(cancelledEvents, plannedEvents...)
	if err := s.AppendEventsTx(ctx, tx, events); err != nil {
		return nil, err
	}
	// events[0+len(priorQueued)] is the validation.planned anchor; requested
	// events follow 1:1 with runs so projections read REAL appended seqs.
	plannedEvent := events[len(priorQueued)]
	for i, run := range runs {
		reqEvent := events[len(priorQueued)+1+i]
		if err := s.insertPlanTx(ctx, tx, plannedEvent, plan); err != nil {
			return nil, err
		}
		if err := s.insertRunTx(ctx, tx, reqEvent, run); err != nil {
			return nil, err
		}
	}
	for _, runID := range priorQueued {
		if _, err := tx.Exec(ctx,
			`UPDATE ctrl.validation_runs SET state='cancelled', finished_at=now() WHERE id=$1 AND state IN ('queued','dispatched')`,
			runID); err != nil {
			return nil, fmt.Errorf("cancel prior run %s: %w", runID, err)
		}
	}
	return events, nil
}

func (s *Store) insertPlanTx(ctx context.Context, tx pgx.Tx, plannedEvent *domain.Event, plan *domain.ValidationPlan) error {
	tiersJSON, kindsJSON, policyJSON, err := planProjectionJSON(plan)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.validation_plans (tenant_id, id, seq, candidate_id, policy, tiers,
		   required_evidence_kinds, inputs_hash, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state WHERE ctrl.validation_plans.seq < EXCLUDED.seq`,
		plan.TenantID, plan.ID, plannedEvent.Seq, plan.CandidateID, policyJSON, tiersJSON,
		kindsJSON, plan.InputsHash, string(plan.State), plan.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert plan projection: %w", err)
	}
	return nil
}

func (s *Store) insertRunTx(ctx context.Context, tx pgx.Tx, reqEvent *domain.Event, run *domain.ValidationRun) error {
	specJSON, err := json.Marshal(run.JobSpec)
	if err != nil {
		return fmt.Errorf("marshal job spec: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.validation_runs (tenant_id, id, seq, plan_id, candidate_id, tier, job_spec,
		   attempt, pool, est_duration_ms, est_cost_millicents, priority, fence_token, timeout_ms,
		   state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state
		 WHERE ctrl.validation_runs.seq < EXCLUDED.seq`,
		run.TenantID, run.ID, reqEvent.Seq, run.PlanID, run.CandidateID, run.Tier,
		specJSON, run.Attempt, run.Pool, run.EstDurationMS, run.EstCostMillicents, run.Priority,
		run.FenceToken, run.TimeoutMS, string(run.State), run.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert run projection: %w", err)
	}
	return nil
}

// planProjectionJSON marshals the three jsonb columns of validation_plans.
func planProjectionJSON(plan *domain.ValidationPlan) (tiersJSON, kindsJSON, policyJSON []byte, err error) {
	tiersJSON, err = json.Marshal(plan.Tiers)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal tiers: %w", err)
	}
	kindsJSON, err = json.Marshal(toAnySlice(plan.RequiredEvidenceKinds))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal required kinds: %w", err)
	}
	policyJSON, err = json.Marshal(plan.Policy)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal plan policy: %w", err)
	}
	return tiersJSON, kindsJSON, policyJSON, nil
}
