package api

import (
	"context"

	"sauron.dev/sauron/control-plane/internal/cluster"
	"sauron.dev/sauron/control-plane/internal/store"
)

// assignCluster runs the clustering engine against the repo's active
// clusters, returning the assignment plus the representative it joined (or
// the candidate itself when unassigned). Unassignable candidates start a
// fresh singleton cluster; failures degrade to unclustered submission
// (clustering influences pacing, never correctness).
func assignCluster(st *store.Store, tenantID, repo, candidateID string, changedPaths []string, priority float64) (cluster.Assignment, string) {
	none := cluster.Assignment{StrategyVersion: cluster.StrategyVersionV0}
	if st == nil {
		return none, ""
	}
	ctx := context.Background()
	clusters, err := st.ActiveClustersForRepo(ctx, tenantID, repo)
	if err != nil {
		return none, ""
	}
	members := make([]cluster.ActiveCluster, 0, len(clusters))
	for _, snap := range clusters {
		active := cluster.ActiveCluster{ID: snap.ID, RepoID: snap.Repo}
		for _, m := range snap.Members {
			if m.Member.ID == snap.RepCandidateID {
				active.Rep = m.Member
				continue
			}
			active.Members = append(active.Members, m)
		}
		members = append(members, active)
	}
	candidate := cluster.Member{
		ID:           candidateID,
		ChangedPaths: changedPaths,
		Priority:     priority,
	}
	assignment := cluster.Assign(candidate, members)
	if !assignment.Joined {
		return assignment, ""
	}
	for _, snap := range clusters {
		if snap.ID == assignment.ClusterID {
			return assignment, snap.RepCandidateID
		}
	}
	return assignment, ""
}
