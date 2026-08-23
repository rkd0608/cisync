package cluster

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

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
