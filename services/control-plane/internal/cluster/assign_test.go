package cluster

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestAssignJoinsAboveThresholds(t *testing.T) {
	rep := member("rep",
		"services/cart/cart.go", "services/cart/totals.go", "services/cart/pricing.go", "services/cart/tax.go")
	newC := member("new",
		"services/cart/cart.go", "services/cart/totals.go", "services/cart/pricing.go", "services/cart/shipping.go")
	got := Assign(newC, []ActiveCluster{singleCluster(rep)})
	require.True(t, got.Joined)
	require.Equal(t, "clus_1", got.ClusterID)
	require.GreaterOrEqual(t, got.PathOverlap, PathOverlapThreshold)
	require.GreaterOrEqual(t, got.TrigramSimilarity, TrigramThreshold)
	require.NotEmpty(t, got.RelationToRep)
	require.Equal(t, StrategyVersionV0, got.StrategyVersion)
}

func TestAssignRejectsBelowThresholds(t *testing.T) {
	rep := member("rep", "payments/gateway.go", "payments/refund.go")
	newC := member("new", "docs/readme.md")
	got := Assign(newC, []ActiveCluster{singleCluster(rep)})
	require.False(t, got.Joined)
	require.Empty(t, got.ClusterID)
	require.Empty(t, got.RelationToRep)
}

func TestAssignRelationClassification(t *testing.T) {
	repPaths := []string{"services/cart/cart.go", "services/cart/totals.go"}

	cases := []struct {
		name string
		newC Member
		rep  Member
		want string
	}{
		{
			"duplicate: high symbol overlap and trigram",
			Member{
				ID: "new", ChangedPaths: repPaths,
				ChangedSymbols: []string{"Alpha", "Beta", "Gamma"},
			},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Alpha", "Beta", "Gamma"}},
			RelationDuplicateOf,
		},
		{
			"prerequisite: declared dependency on rep",
			Member{
				ID: "new", ChangedPaths: repPaths,
				ChangedSymbols: []string{"Zeta"}, DependsOn: []string{"rep"},
			},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Omega"}},
			RelationPrerequisite,
		},
		{
			"conflicting: same files, disjoint symbols, no dep edge",
			Member{ID: "new", ChangedPaths: repPaths, ChangedSymbols: []string{"Kappa"}},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Lambda"}},
			RelationConflicting,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Assign(tc.newC, []ActiveCluster{singleCluster(tc.rep)})
			require.True(t, got.Joined, "fixture must clear join thresholds")
			require.Equal(t, tc.want, got.RelationToRep)
		})
	}
}

func TestAssignBestClusterDeterministicTieBreak(t *testing.T) {
	paths := []string{"services/x/a.go", "services/x/b.go"}
	cA := ActiveCluster{ID: "clus_a", RepoID: "acme/payments", Rep: member("repa", paths...)}
	cB := ActiveCluster{ID: "clus_b", RepoID: "acme/payments", Rep: member("repb", paths...)}
	newC := member("new", paths...)

	first := Assign(newC, []ActiveCluster{cB, cA})
	second := Assign(newC, []ActiveCluster{cA, cB})
	require.True(t, first.Joined)
	require.Equal(t, first, second, "cluster iteration order must not change the decision")
	require.Equal(t, "clus_a", first.ClusterID, "equal scores resolve to smaller cluster id")
}

func TestPropertyJoinCriterionSymmetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := Member{ID: "a", ChangedPaths: genPaths(t), ChangedSymbols: genSym(t)}
		b := Member{ID: "b", ChangedPaths: genPaths(t), ChangedSymbols: genSym(t)}

		ab := Assign(a, []ActiveCluster{singleCluster(b)})
		ba := Assign(b, []ActiveCluster{singleCluster(a)})
		require.Equal(t, ab.Joined, ba.Joined,
			"join thresholds are symmetric: %v vs %v", ab.PathOverlap, ba.PathOverlap)
		if ab.Joined && ba.Joined {
			require.InDelta(t, ab.PathOverlap, ba.PathOverlap, 1e-12)
			require.InDelta(t, ab.TrigramSimilarity, ba.TrigramSimilarity, 1e-12)
		}
	})
}

func TestPropertyAssignDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := Member{ID: "c", ChangedPaths: genPaths(t), ChangedSymbols: genSym(t)}
		n := rapid.IntRange(0, 3).Draw(t, "clusters")
		clusters := make([]ActiveCluster, 0, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("clus_%02d", i)
			clusters = append(clusters, ActiveCluster{
				ID: id, RepoID: "acme/payments",
				Rep: Member{ID: "rep" + id, ChangedPaths: genPaths(t), ChangedSymbols: genSym(t)},
			})
		}
		a := Assign(c, clusters)
		// reverse order
		rev := make([]ActiveCluster, len(clusters))
		for i, cl := range clusters {
			rev[len(clusters)-1-i] = cl
		}
		require.Equal(t, a, Assign(c, rev))
	})
}
