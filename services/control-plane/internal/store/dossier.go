package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// GetCluster returns a cluster with its member edges within tenant.
func (s *Store) GetCluster(ctx context.Context, tenantID, clusterID string) (*domain.Cluster, []domain.ClusterMember, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, tenant_id, repo, rep_candidate_id, member_count, state, strategy_version, created_at
		 FROM ctrl.clusters WHERE id=$1 AND tenant_id=$2`, clusterID, tenantID)
	var c domain.Cluster
	var state string
	err := row.Scan(&c.ID, &c.TenantID, &c.Repo, &c.RepCandidateID, &c.MemberCount, &state, &c.StrategyVersion, &c.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, fmt.Errorf("get cluster: %w", err)
	}
	c.State = domain.ClusterState(state)
	rows, err := s.Pool.Query(ctx,
		`SELECT candidate_id, relation_to_rep, similarity_score FROM ctrl.cluster_members WHERE cluster_id=$1`,
		clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("cluster members: %w", err)
	}
	defer rows.Close()
	var members []domain.ClusterMember
	for rows.Next() {
		var m domain.ClusterMember
		var rel string
		if err := rows.Scan(&m.CandidateID, &rel, &m.SimilarityScore); err != nil {
			return nil, nil, fmt.Errorf("scan member: %w", err)
		}
		m.RelationToRep = domain.Relation(rel)
		members = append(members, m)
	}
	return &c, members, rows.Err()
}

// AcceptedEvidence is a read model row for dossiers.
type AcceptedEvidence struct {
	ID        string
	RunID     string
	Attempt   int
	Candidate string
	Kind      string
	Verdict   string
	Digests   []string
	Meta      map[string]any
}

// AcceptedEvidenceForCandidate lists accepted evidence records.
func (s *Store) AcceptedEvidenceForCandidate(ctx context.Context, tenantID, candidateID string) ([]AcceptedEvidence, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, run_id, attempt, candidate_id, kind, verdict, digests
		 FROM ctrl.evidence_records WHERE tenant_id=$1 AND candidate_id=$2 AND status='accepted'
		 ORDER BY accepted_at`, tenantID, candidateID)
	if err != nil {
		return nil, fmt.Errorf("accepted evidence: %w", err)
	}
	defer rows.Close()
	var out []AcceptedEvidence
	for rows.Next() {
		var e AcceptedEvidence
		var digestsRaw []byte
		if err := rows.Scan(&e.ID, &e.RunID, &e.Attempt, &e.Candidate, &e.Kind, &e.Verdict, &digestsRaw); err != nil {
			return nil, fmt.Errorf("scan evidence: %w", err)
		}
		if err := json.Unmarshal(digestsRaw, &e.Digests); err != nil {
			return nil, fmt.Errorf("unmarshal digests: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DecisionRow is the persisted decision projection.
type DecisionRow struct {
	ID         string
	SubjectID  string
	Verb       string
	Confidence float64
	Policy     domain.PolicyRef
	Summary    string
	RenderedAt time.Time
}

// LatestDecisionForCandidate returns the newest decision over the candidate.
func (s *Store) LatestDecisionForCandidate(ctx context.Context, tenantID, candidateID string) (*DecisionRow, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, subject_id, verb, confidence, policy, explanation->>'summary', rendered_at
		 FROM ctrl.decisions WHERE tenant_id=$1 AND subject_type='candidate' AND subject_id=$2
		 ORDER BY rendered_at DESC LIMIT 1`, tenantID, candidateID)
	var d DecisionRow
	var policyRaw []byte
	err := row.Scan(&d.ID, &d.SubjectID, &d.Verb, &d.Confidence, &policyRaw, &d.Summary, &d.RenderedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("latest decision: %w", err)
	}
	if err := json.Unmarshal(policyRaw, &d.Policy); err != nil {
		return nil, fmt.Errorf("unmarshal decision policy: %w", err)
	}
	return &d, nil
}

// InputsHashForCandidate resolves the plan inputs hash backing a dossier.
func (s *Store) InputsHashForCandidate(ctx context.Context, tenantID, candidateID string) (string, error) {
	var h string
	err := s.Pool.QueryRow(ctx,
		`SELECT inputs_hash FROM ctrl.validation_plans WHERE tenant_id=$1 AND candidate_id=$2
		 ORDER BY seq DESC LIMIT 1`, tenantID, candidateID).Scan(&h)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("inputs hash: %w", err)
	}
	return h, nil
}
