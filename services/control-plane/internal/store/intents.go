package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// OverlappingIntents finds active intents whose owned surfaces intersect any
// of the given surfaces (GIN-assisted admission hot path).
func (s *Store) OverlappingIntents(ctx context.Context, tenantID, repo string, surfaces []string) ([]domain.ConflictRef, error) {
	if len(surfaces) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT id, repo FROM ctrl.intents
		 WHERE tenant_id=$1 AND state NOT IN ('completed','rejected') AND owned_surfaces && $2 LIMIT 50`,
		tenantID, surfaces,
	)
	if err != nil {
		return nil, fmt.Errorf("overlap search: %w", err)
	}
	defer rows.Close()
	var out []domain.ConflictRef
	for rows.Next() {
		var id, otherRepo string
		if err := rows.Scan(&id, &otherRepo); err != nil {
			return nil, fmt.Errorf("overlap scan: %w", err)
		}
		out = append(out, domain.ConflictRef{
			IntentID:       id,
			Relation:       "overlapping",
			Owner:          otherRepo,
			Recommendation: "coordinate",
		})
	}
	return out, rows.Err()
}

// CreateIntentTx persists intent + change-scope lease projections and appends
// intent.declared + lease.granted in one transaction.
func CreateIntentTx(ctx context.Context, tx pgx.Tx, s *Store, intent *domain.Intent, lease *domain.Lease, conflicts []domain.ConflictRef) ([]*domain.Event, error) {
	corr := domain.NewCorrelationID()
	actor := domain.EventActor{Kind: string(domain.ActorAgent), ID: "api"}

	intentEvent, err := domain.NewEvent(intent.TenantID,
		domain.AggregateRef{Type: string(domain.AggIntent), ID: intent.ID},
		"intent.declared", "", corr, actor, map[string]any{
			"intent_id":           intent.ID,
			"goal":                intent.Declared.Goal,
			"owned_surfaces":      toAnySlice(intent.Declared.OwnedSurfaces),
			"risk_class":          string(intent.Declared.RiskClass),
			"origin":              string(intent.Declared.Origin),
			"agent_lineage":       toAnySlice(intent.Declared.AgentLineage),
			"resolved_policy":     map[string]any{"policy_id": intent.Declared.ResolvedPolicy.PolicyID, "policy_version": intent.Declared.ResolvedPolicy.Version},
			"compute_budget":      intent.Declared.ComputeBudget,
			"acceptance_criteria": toAnySlice(intent.Declared.AcceptanceCriteria),
			"constraints":         toAnySlice(intent.Declared.Constraints),
			// Reserved first-candidate slot: roots the §3b lifecycle trace.
			"initial_candidate_id": intent.InitialCandidateID,
		})
	if err != nil {
		return nil, err
	}

	conflictsAny := make([]any, 0, len(conflicts))
	for _, c := range conflicts {
		conflictsAny = append(conflictsAny, c)
	}
	leaseEvent, err := domain.NewEvent(lease.TenantID,
		domain.AggregateRef{Type: string(domain.AggLease), ID: lease.ID},
		"lease.granted", intentEvent.ID, corr, actor, map[string]any{
			"lease_id":          lease.ID,
			"intent_id":         lease.IntentID,
			"scope":             map[string]any{"kind": string(lease.Scope.Kind), "surfaces": toAnySlice(lease.Scope.Surfaces)},
			"holder":            lease.Holder,
			"budget":            lease.Budget,
			"ttl_expires_at":    lease.TTLExpiresAt.Format(time.RFC3339),
			"conflicts":         conflictsAny,
			"required_evidence": toAnySlice(lease.RequiredEvidence),
			// Same reserved slot so the granted lease stays inside the trace.
			"initial_candidate_id": intent.InitialCandidateID,
		})
	if err != nil {
		return nil, err
	}

	if err := s.AppendEventsTx(ctx, tx, []*domain.Event{intentEvent, leaseEvent}); err != nil {
		return nil, err
	}

	constraintsJSON, err := json.Marshal(toAnySlice(intent.Declared.Constraints))
	if err != nil {
		return nil, fmt.Errorf("marshal constraints: %w", err)
	}
	criteriaJSON, err := json.Marshal(toAnySlice(intent.Declared.AcceptanceCriteria))
	if err != nil {
		return nil, fmt.Errorf("marshal acceptance criteria: %w", err)
	}
	lineageJSON, err := json.Marshal(toAnySlice(intent.Declared.AgentLineage))
	if err != nil {
		return nil, fmt.Errorf("marshal lineage: %w", err)
	}
	policyJSON, err := json.Marshal(intent.Declared.ResolvedPolicy)
	if err != nil {
		return nil, fmt.Errorf("marshal policy ref: %w", err)
	}
	budgetJSON, err := json.Marshal(intent.Declared.ComputeBudget)
	if err != nil {
		return nil, fmt.Errorf("marshal budget: %w", err)
	}
	var deadline any
	if intent.Declared.Deadline != nil {
		deadline = *intent.Declared.Deadline
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.intents (tenant_id, id, seq, state, goal, repo, base_ref, base_snapshot,
		   owned_surfaces, constraints, acceptance_criteria, risk_class, deadline, origin,
		   agent_lineage, resolved_policy, compute_budget, created_at, initial_candidate_id, pr_number)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		 ON CONFLICT (id) DO UPDATE SET
		   seq=EXCLUDED.seq, state=EXCLUDED.state WHERE ctrl.intents.seq < EXCLUDED.seq`,
		intent.TenantID, intent.ID, intentEvent.Seq, string(intent.State), intent.Declared.Goal,
		intent.Declared.Repo, intent.Declared.BaseRef, intent.Declared.BaseSnapshot,
		toTextSlice(intent.Declared.OwnedSurfaces), constraintsJSON, criteriaJSON,
		string(intent.Declared.RiskClass), deadline, string(intent.Declared.Origin),
		lineageJSON, policyJSON, budgetJSON, intent.CreatedAt, intent.InitialCandidateID,
		intent.PRNumber,
	); err != nil {
		return nil, fmt.Errorf("insert intent projection: %w", err)
	}

	var envTemplate *string
	if lease.Scope.EnvTemplate != "" {
		envTemplate = &lease.Scope.EnvTemplate
	}
	reqJSON, err := json.Marshal(toAnySlice(lease.RequiredEvidence))
	if err != nil {
		return nil, fmt.Errorf("marshal required evidence: %w", err)
	}
	budgetLeaseJSON, err := json.Marshal(lease.Budget)
	if err != nil {
		return nil, fmt.Errorf("marshal lease budget: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.leases (tenant_id, id, seq, intent_id, scope_kind, surfaces, env_template,
		   holder, budget, ttl_expires_at, renewal_count, queue_position, eta_seconds,
		   required_evidence, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 ON CONFLICT (id) DO UPDATE SET
		   seq=EXCLUDED.seq, state=EXCLUDED.state WHERE ctrl.leases.seq < EXCLUDED.seq`,
		lease.TenantID, lease.ID, leaseEvent.Seq, lease.IntentID, string(lease.Scope.Kind),
		toTextSlice(lease.Scope.Surfaces), envTemplate, lease.Holder, budgetLeaseJSON,
		lease.TTLExpiresAt, lease.RenewalCount, lease.QueuePosition, lease.EtaSeconds,
		reqJSON, string(lease.State), lease.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert lease projection: %w", err)
	}
	return []*domain.Event{intentEvent, leaseEvent}, nil
}

func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

func toTextSlice(in []string) []string { return in }

// GetIntent loads an intent projection within the caller's tenant only
// (invariant I-14).
func (s *Store) GetIntent(ctx context.Context, tenantID, intentID string) (*domain.Intent, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, tenant_id, state, goal, repo, base_ref, base_snapshot, owned_surfaces,
		        risk_class, deadline, origin, created_at, closed_at, initial_candidate_id
		 FROM ctrl.intents WHERE id=$1 AND tenant_id=$2`, intentID, tenantID)
	var i domain.Intent
	var state, risk, origin string
	var deadline, closedAt *time.Time
	err := row.Scan(&i.ID, &i.TenantID, &state, &i.Declared.Goal, &i.Declared.Repo,
		&i.Declared.BaseRef, &i.Declared.BaseSnapshot, &i.Declared.OwnedSurfaces,
		&risk, &deadline, &origin, &i.CreatedAt, &closedAt, &i.InitialCandidateID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get intent: %w", err)
	}
	i.State = domain.IntentState(state)
	i.Declared.RiskClass = domain.RiskClass(risk)
	i.Declared.Origin = domain.IntentOrigin(origin)
	i.Declared.Deadline = deadline
	i.ClosedAt = closedAt
	return &i, nil
}
