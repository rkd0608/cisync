package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/cluster"
)

// ClusterSnapshot is the clustering view of one active cluster, joined from
// the cluster projections and its members' candidate rows.
type ClusterSnapshot struct {
	ID             string
	TenantID       string
	Repo           string
	RepCandidateID string
	Members        []cluster.MemberWithRelation
}

// ActiveClustersForRepo loads the active clusters of a repo for assignment
// decisions at candidate submission.
func (s *Store) ActiveClustersForRepo(ctx context.Context, tenantID, repo string) ([]ClusterSnapshot, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, rep_candidate_id FROM ctrl.clusters
		 WHERE tenant_id=$1 AND repo=$2 AND state='active'`, tenantID, repo)
	if err != nil {
		return nil, fmt.Errorf("active clusters: %w", err)
	}
	type head struct {
		id, rep string
	}
	var heads []head
	for rows.Next() {
		var h head
		if err := rows.Scan(&h.id, &h.rep); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan cluster head: %w", err)
		}
		heads = append(heads, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate clusters: %w", err)
	}

	out := make([]ClusterSnapshot, 0, len(heads))
	for _, h := range heads {
		snap := ClusterSnapshot{ID: h.id, TenantID: tenantID, Repo: repo, RepCandidateID: h.rep}
		if err := s.appendMembers(ctx, &snap); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

// ClusterForCandidate returns the active cluster containing the candidate,
// or ok=false when the candidate is unclustered.
func (s *Store) ClusterForCandidate(ctx context.Context, tenantID, candidateID string) (*ClusterSnapshot, bool, error) {
	var clusterID *string
	err := s.Pool.QueryRow(ctx,
		`SELECT cluster_id FROM ctrl.candidates WHERE id=$1 AND tenant_id=$2`,
		candidateID, tenantID).Scan(&clusterID)
	if err != nil {
		if err == pgx.ErrNoRows || clusterID == nil {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cluster for candidate: %w", err)
	}
	if clusterID == nil || *clusterID == "" {
		return nil, false, nil
	}
	snap := ClusterSnapshot{ID: *clusterID, TenantID: tenantID}
	err = s.Pool.QueryRow(ctx,
		`SELECT repo, rep_candidate_id FROM ctrl.clusters WHERE id=$1 AND tenant_id=$2`,
		snap.ID, tenantID).Scan(&snap.Repo, &snap.RepCandidateID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("load cluster %s: %w", snap.ID, err)
	}
	if err := s.appendMembers(ctx, &snap); err != nil {
		return nil, false, err
	}
	return &snap, true, nil
}

func (s *Store) appendMembers(ctx context.Context, snap *ClusterSnapshot) error {
	repMember, err := s.memberFromCandidate(ctx, snap.TenantID, snap.RepCandidateID)
	if err != nil {
		return err
	}
	snap.Members = append(snap.Members, cluster.MemberWithRelation{Member: repMember})

	// WHY two passes: hydrating members while the rows iterator is open
	// checks out a SECOND connection per nested query; under concurrency
	// every holder waits for a free conn and the pool gridlocks (W3 storm:
	// 63/64 conns parked mid-iteration). Drain first, hydrate after.
	rows, err := s.Pool.Query(ctx,
		`SELECT candidate_id, relation_to_rep FROM ctrl.cluster_members WHERE cluster_id=$1`, snap.ID)
	if err != nil {
		return fmt.Errorf("cluster members: %w", err)
	}
	type memberRef struct {
		id       string
		relation string
	}
	refs := make([]memberRef, 0, 8)
	for rows.Next() {
		var ref memberRef
		if err := rows.Scan(&ref.id, &ref.relation); err != nil {
			rows.Close()
			return fmt.Errorf("scan cluster member: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate cluster members: %w", err)
	}
	rows.Close()

	for _, ref := range refs {
		member, err := s.memberFromCandidate(ctx, snap.TenantID, ref.id)
		if err != nil {
			return err
		}
		snap.Members = append(snap.Members, cluster.MemberWithRelation{Member: member, RelationToRep: ref.relation})
	}
	return nil
}

// memberFromCandidate builds the clustering Member view from the candidate
// projection (paths, priority, submission seq).
func (s *Store) memberFromCandidate(ctx context.Context, tenantID, candidateID string) (cluster.Member, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, changed_paths, priority_score, seq FROM ctrl.candidates
		 WHERE id=$1 AND tenant_id=$2`, candidateID, tenantID)
	var m cluster.Member
	err := row.Scan(&m.ID, &m.ChangedPaths, &m.Priority, &m.CreatedSeq)
	if err != nil {
		return cluster.Member{}, fmt.Errorf("cluster member candidate %s: %w", candidateID, err)
	}
	return m, nil
}
