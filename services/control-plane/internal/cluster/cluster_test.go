package cluster

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestJaccardKnownValues(t *testing.T) {
	require.InDelta(t, 1.0, Jaccard([]string{"a", "b"}, []string{"b", "a"}), 1e-12)
	require.InDelta(t, 1.0/3.0, Jaccard([]string{"a", "b"}, []string{"b", "c"}), 1e-12)
	require.InDelta(t, 0.0, Jaccard([]string{"a"}, []string{"b"}), 1e-12)
	require.InDelta(t, 0.0, Jaccard(nil, nil), 1e-12, "empty union must not look identical")
}

func TestTrigramSimilarityKnownValues(t *testing.T) {
	require.InDelta(t, 1.0, TrigramSimilarity("services/cart/cart.go", "services/cart/cart.go"), 1e-12)
	sim := TrigramSimilarity("services/cart/cart.go", "services/cart/totals.go")
	require.Greater(t, sim, 0.5)
	less := TrigramSimilarity("services/cart/cart.go", "docs/readme.md")
	require.Less(t, less, sim)
}

func member(id string, paths ...string) Member {
	return Member{ID: id, ChangedPaths: paths, ChangedSymbols: []string{id + "_sym"}, CreatedSeq: 1}
}

func singleCluster(rep Member, members ...MemberWithRelation) ActiveCluster {
	return ActiveCluster{ID: "clus_1", RepoID: "acme/payments", Rep: rep, Members: members}
}

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
	}{
		{
			"duplicate: high symbol overlap and trigram",
			Member{
				ID: "new", ChangedPaths: repPaths,
				ChangedSymbols: []string{"Alpha", "Beta", "Gamma"},
			},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Alpha", "Beta", "Gamma"}},
		},
		{
			"prerequisite: declared dependency on rep",
			Member{
				ID: "new", ChangedPaths: repPaths,
				ChangedSymbols: []string{"Zeta"}, DependsOn: []string{"rep"},
			},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Omega"}},
		},
		{
			"conflicting: same files, disjoint symbols, no dep edge",
			Member{ID: "new", ChangedPaths: repPaths, ChangedSymbols: []string{"Kappa"}},
			Member{ID: "rep", ChangedPaths: repPaths, ChangedSymbols: []string{"Lambda"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Assign(tc.newC, []ActiveCluster{singleCluster(tc.rep)})
			require.True(t, got.Joined, "fixture must clear join thresholds")
			switch tc.name {
			case "duplicate: high symbol overlap and trigram":
				require.Equal(t, RelationDuplicateOf, got.RelationToRep)
			case "prerequisite: declared dependency on rep":
				require.Equal(t, RelationPrerequisite, got.RelationToRep)
			default:
				require.Equal(t, RelationConflicting, got.RelationToRep)
			}
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

func TestElectRepresentativePriorityAgeULID(t *testing.T) {
	_, ok := ElectRepresentative(nil)
	require.False(t, ok)

	lowOld := Member{ID: "low_old", Priority: 1, CreatedSeq: 10}
	highNew := Member{ID: "high_new", Priority: 5, CreatedSeq: 99}
	eqA := Member{ID: "eq_a", Priority: 3, CreatedSeq: 50}
	eqB := Member{ID: "eq_b", Priority: 3, CreatedSeq: 40}

	best, ok := ElectRepresentative([]Member{lowOld, highNew, eqA, eqB})
	require.True(t, ok)
	require.Equal(t, "high_new", best.ID)

	best, _ = ElectRepresentative([]Member{eqA, lowOld, eqB})
	require.Equal(t, "eq_b", best.ID, "priority tie → older seq wins")

	best, _ = ElectRepresentative([]Member{
		{ID: "z", Priority: 3, CreatedSeq: 40},
		{ID: "a", Priority: 3, CreatedSeq: 40},
	})
	require.Equal(t, "a", best.ID, "full tie → lexicographically smallest id")
}

func TestSupersedeDecisionsTable(t *testing.T) {
	rep := member("rep")
	cl := ActiveCluster{
		ID: "clus_1", RepoID: "r", Rep: rep,
		Members: []MemberWithRelation{
			{Member: member("dup"), RelationToRep: RelationDuplicateOf},
			{Member: member("alt"), RelationToRep: RelationAlternativeOf},
			{Member: member("comp"), RelationToRep: RelationComposable},
			{Member: member("conf"), RelationToRep: RelationConflicting},
			{Member: member("prereq"), RelationToRep: RelationPrerequisite},
		},
	}

	got := SupersedeDecisions(cl, EventEligible)
	require.Equal(t, []SupersedeDecision{
		{CandidateID: "alt", Action: ActionSupersede, Reason: ReasonTournamentLoser},
		{CandidateID: "dup", Action: ActionSupersede, Reason: ReasonDominatedDuplicate},
	}, got, "eligible: duplicates dominated, alternatives lose tournament; others untouched")

	got = SupersedeDecisions(cl, EventRejected)
	require.Equal(t, []SupersedeDecision{
		{CandidateID: "prereq", Action: ActionBlock, Reason: ReasonPrerequisiteFailed},
	}, got, "rejected: only prerequisites auto-block, never superseded")

	require.Empty(t, SupersedeDecisions(cl, EventCancelled))
	require.Empty(t, SupersedeDecisions(ActiveCluster{Rep: rep}, EventEligible))
}

// --- property tests ---

var genPath = rapid.SampledFrom([]string{
	"services/cart/cart.go", "services/cart/totals.go", "services/auth/login.go",
	"payments/refund.go", "docs/readme.md", "infra/main.tf",
})

func genPaths(t *rapid.T) []string { return rapid.SliceOfN(genPath, 0, 4).Draw(t, "paths") }

func genSym(t *rapid.T) []string {
	return rapid.SliceOfN(rapid.SampledFrom([]string{"Alpha", "Beta", "Gamma", "Delta"}), 0, 4).Draw(t, "syms")
}

func TestPropertySimilaritySymmetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b := genPaths(t), genPaths(t)
		require.Equal(t, Jaccard(a, b), Jaccard(b, a))
		sa, sb := JoinPaths(a), JoinPaths(b)
		require.Equal(t, TrigramSimilarity(sa, sb), TrigramSimilarity(sb, sa))
	})
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

func TestPropertyRepresentativeIsAlwaysAMember(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 8).Draw(t, "n")
		members := make([]Member, 0, n)
		for i := 0; i < n; i++ {
			members = append(members, Member{
				ID:         fmt.Sprintf("m%02d", i),
				Priority:   float64(rapid.IntRange(-9, 9).Draw(t, "prio")),
				CreatedSeq: int64(rapid.IntRange(0, 100).Draw(t, "seq")),
			})
		}
		best, ok := ElectRepresentative(members)
		require.True(t, ok)
		found := false
		for _, m := range members {
			if m.ID == best.ID {
				found = true
			}
		}
		require.True(t, found, "representative must be a live member of the input set")

		// determinism under permutation
		reversed := make([]Member, len(members))
		for i, m := range members {
			reversed[len(members)-1-i] = m
		}
		again, _ := ElectRepresentative(reversed)
		require.Equal(t, best, again)
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
