package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// CandidateAcceptedData bundles the submit-candidate response payload.
type CandidateAcceptedData struct {
	Candidate *domain.Candidate
	Plan      *domain.ValidationPlan
	Runs      []*domain.ValidationRun
	LeaseID   string
}

// LiveCandidateExists reports whether a non-terminal candidate already covers
// (intent_id, head_sha) — duplicate_sha conflict (EC-019).
func (s *Store) LiveCandidateExists(ctx context.Context, tenantID, intentID, headSHA string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM ctrl.candidates
		 WHERE tenant_id=$1 AND intent_id=$2 AND head_sha=$3 AND state NOT IN ('superseded','cancelled'))`,
		tenantID, intentID, headSHA,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("live candidate check: %w", err)
	}
	return exists, nil
}

// SubmitCandidateTx persists candidate + plan + queued runs and appends
// candidate.submitted, validation.planned and validation.requested atomically.
func SubmitCandidateTx(ctx context.Context, tx pgx.Tx, s *Store, cand *domain.Candidate, plan *domain.ValidationPlan, runs []*domain.ValidationRun) ([]*domain.Event, error) {
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
		   state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 ON CONFLICT (id) DO UPDATE SET seq=EXCLUDED.seq, state=EXCLUDED.state
		 WHERE ctrl.candidates.seq < EXCLUDED.seq`,
		cand.TenantID, cand.ID, submittedEvent.Seq, cand.IntentID, cand.Submitter, cand.PatchRef,
		cand.HeadSHA, cand.BaseSHA, toTextSlice(cand.ChangedPaths), cand.EstCostMillicents,
		cand.PriorityScore, nullableString(cand.ClusterID), relationOrNull(cand.RelationToRep),
		string(cand.State), cand.CreatedAt,
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
	return events, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func relationOrNull(r *domain.Relation) any {
	if r == nil {
		return nil
	}
	return string(*r)
}

// ListCandidates returns candidate summaries for an intent within tenant.
func (s *Store) ListCandidates(ctx context.Context, tenantID, intentID string) ([]*domain.Candidate, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, intent_id, state, head_sha, base_sha, priority_score, cluster_id, relation_to_rep, created_at
		 FROM ctrl.candidates WHERE tenant_id=$1 AND intent_id=$2 ORDER BY created_at`, tenantID, intentID)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()
	var out []*domain.Candidate
	for rows.Next() {
		var c domain.Candidate
		var state string
		var clusterID *string
		var rel *string
		if err := rows.Scan(&c.ID, &c.IntentID, &state, &c.HeadSHA, &c.BaseSHA,
			&c.PriorityScore, &clusterID, &rel, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		c.State = domain.CandidateState(state)
		c.ClusterID = derefString(clusterID)
		if rel != nil {
			r := domain.Relation(*rel)
			c.RelationToRep = &r
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// GetCandidate loads one candidate within tenant.
func (s *Store) GetCandidate(ctx context.Context, tenantID, candidateID string) (*domain.Candidate, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, intent_id, state, head_sha, base_sha, patch_ref, priority_score, cluster_id,
		        relation_to_rep, est_cost_millicents, created_at
		 FROM ctrl.candidates WHERE id=$1 AND tenant_id=$2`, candidateID, tenantID)
	var c domain.Candidate
	var state string
	var clusterID *string
	var rel *string
	err := row.Scan(&c.ID, &c.IntentID, &state, &c.HeadSHA, &c.BaseSHA, &c.PatchRef,
		&c.PriorityScore, &clusterID, &rel, &c.EstCostMillicents, &c.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get candidate: %w", err)
	}
	c.State = domain.CandidateState(state)
	c.ClusterID = derefString(clusterID)
	if rel != nil {
		r := domain.Relation(*rel)
		c.RelationToRep = &r
	}
	return &c, nil
}

// QueuePositionForCandidate computes the candidate's queue position among
// queued runs of its intent (nil when nothing queued).
func (s *Store) QueuePositionForCandidate(ctx context.Context, tenantID, candidateID string) (*int, error) {
	var pos *int
	err := s.Pool.QueryRow(ctx,
		`WITH ranked AS (
		   SELECT id, ROW_NUMBER() OVER (ORDER BY priority DESC, created_at) - 1 AS pos
		   FROM ctrl.validation_runs WHERE tenant_id=$1 AND state='queued'
		 ) SELECT pos FROM ranked WHERE id IN (SELECT id FROM ctrl.validation_runs WHERE candidate_id=$2 LIMIT 1)`,
		tenantID, candidateID,
	).Scan(&pos)
	if err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("queue position: %w", err)
	}
	return pos, nil
}

// PlanSummaryForCandidate returns the active plan's tier summary.
func (s *Store) ActivePlanForCandidate(ctx context.Context, tenantID, candidateID string) (*domain.ValidationPlan, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, candidate_id, policy, tiers, required_evidence_kinds, inputs_hash, state, created_at
		 FROM ctrl.validation_plans WHERE tenant_id=$1 AND candidate_id=$2 ORDER BY seq DESC LIMIT 1`,
		tenantID, candidateID)
	var p domain.ValidationPlan
	var policyRaw, tiersRaw, kindsRaw []byte
	var state string
	err := row.Scan(&p.ID, &p.CandidateID, &policyRaw, &tiersRaw, &kindsRaw, &p.InputsHash, &state, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("active plan: %w", err)
	}
	p.TenantID = tenantID
	p.State = domain.PlanState(state)
	var ref domain.PolicyRef
	if err := json.Unmarshal(policyRaw, &ref); err != nil {
		return nil, fmt.Errorf("unmarshal plan policy: %w", err)
	}
	p.Policy = ref
	var tiers []domain.Tier
	if err := json.Unmarshal(tiersRaw, &tiers); err != nil {
		return nil, fmt.Errorf("unmarshal tiers: %w", err)
	}
	p.Tiers = tiers
	var kinds []string
	if err := json.Unmarshal(kindsRaw, &kinds); err != nil {
		return nil, fmt.Errorf("unmarshal kinds: %w", err)
	}
	p.RequiredEvidenceKinds = kinds
	return &p, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
