package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// CandidateAcceptedData bundles the submit-candidate response payload.
type CandidateAcceptedData struct {
	Candidate *domain.Candidate
	Plan      *domain.ValidationPlan
	Runs      []*domain.ValidationRun
	LeaseID   string
}

// LiveCandidateExists reports whether a non-terminal candidate already covers
// (intent_id, head_sha, base_sha) — duplicate_sha conflict (EC-019). The
// base participates because a moved base is a changed input (I-02): the same
// patch on a new base must plan fresh, never be swallowed as a duplicate.
func (s *Store) LiveCandidateExists(ctx context.Context, tenantID, intentID, headSHA, baseSHA string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM ctrl.candidates
		 WHERE tenant_id=$1 AND intent_id=$2 AND head_sha=$3 AND base_sha=$4 AND state NOT IN ('superseded','cancelled'))`,
		tenantID, intentID, headSHA, baseSHA,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("live candidate check: %w", err)
	}
	return exists, nil
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

// LiveCandidateCountByIntent counts non-terminal candidates of an intent —
// the lease-revocation trigger when it drops to zero. The Tx variant lets
// callers read their own uncommitted state changes.
func (s *Store) LiveCandidateCountByIntent(ctx context.Context, tenantID, intentID string) (int, error) {
	return LiveCandidateCountByIntentTx(ctx, s.Pool, tenantID, intentID)
}

// LiveCandidateCountByIntentTx counts within a caller's transaction.
func LiveCandidateCountByIntentTx(ctx context.Context, q Queryer, tenantID, intentID string) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM ctrl.candidates
		 WHERE tenant_id=$1 AND intent_id=$2 AND state NOT IN ('superseded','cancelled','rejected','eligible')`,
		tenantID, intentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("live candidate count: %w", err)
	}
	return n, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// CandidateStateByID returns just the lifecycle state of one candidate —
// the lightweight read the completion gate needs for I-08 absorption.
func (s *Store) CandidateStateByID(ctx context.Context, tenantID, candidateID string) (domain.CandidateState, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT state FROM ctrl.candidates WHERE id=$1 AND tenant_id=$2`, candidateID, tenantID)
	var state string
	if err := row.Scan(&state); err != nil {
		if err == pgx.ErrNoRows {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("candidate state: %w", err)
	}
	return domain.CandidateState(state), nil
}
