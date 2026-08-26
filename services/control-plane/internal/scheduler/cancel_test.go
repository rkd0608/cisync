package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"cisync.dev/cisync/control-plane/internal/cluster"
)

func edge(from, rel, to string) RelationEdge {
	return RelationEdge{FromCandidateID: from, ToCandidateID: to, Relation: rel}
}

func runInfo(id, cand, state string, fence int64) RunInfo {
	return RunInfo{RunID: id, CandidateID: cand, State: state, FenceToken: fence}
}

func TestPropagateEligibleCancelsDuplicatesAndAlternatives(t *testing.T) {
	st := ClusterState{
		Edges: []RelationEdge{
			edge("dup1", cluster.RelationDuplicateOf, "rep"),
			edge("alt1", cluster.RelationAlternativeOf, "rep"),
			edge("comp1", cluster.RelationComposable, "rep"),
			edge("conf1", cluster.RelationConflicting, "rep"),
			edge("unrelated", cluster.RelationDuplicateOf, "other"),
		},
		Runs: []RunInfo{
			runInfo("r_dup", "dup1", StateRunning, 7),
			runInfo("r_alt", "alt1", StateQueued, 3),
			runInfo("r_comp", "comp1", StateRunning, 9),
			runInfo("r_rep_done", "rep", StateSucceededForTest, 5),
		},
	}
	prop := PropagateCancellation(st, "rep", CauseEligible)

	require.Equal(t, []string{"alt1", "dup1"}, idsOf(prop.Candidates))
	require.Equal(t, cluster.ReasonTournamentLoser, prop.Candidates[0].Reason)
	require.Equal(t, cluster.ReasonDominatedDuplicate, prop.Candidates[1].Reason)

	require.Len(t, prop.Runs, 2, "composable member and terminal rep runs untouched")
	require.Equal(t, "r_alt", prop.Runs[0].RunID)
	require.Equal(t, int64(3), prop.Runs[0].FenceToken, "fence token echoed for kill verification")
	require.Equal(t, "r_dup", prop.Runs[1].RunID)
}

func TestPropagateRejectedBlocksPrerequisitesOnly(t *testing.T) {
	st := ClusterState{
		Edges: []RelationEdge{
			edge("depA", cluster.RelationPrerequisite, "base"),
			edge("dupB", cluster.RelationDuplicateOf, "base"),
		},
		Runs: []RunInfo{runInfo("r_dep", "depA", StateDispatched, 2)},
	}
	prop := PropagateCancellation(st, "base", CauseRejected)

	require.Len(t, prop.Candidates, 1)
	require.Equal(t, "depA", prop.Candidates[0].CandidateID)
	require.Equal(t, cluster.ActionBlock, ActionForTest(prop.Candidates[0].Reason))
	require.Equal(t, cluster.ReasonPrerequisiteFailed, prop.Candidates[0].Reason)
	require.Len(t, prop.Runs, 1)
}

func TestPropagateSelfRunsCancelledWithSupersededReason(t *testing.T) {
	st := ClusterState{
		Runs: []RunInfo{
			runInfo("r_self_live", "win", StateRunning, 11),
			runInfo("r_self_done", "win", StateSucceededForTest, 11),
		},
	}
	prop := PropagateCancellation(st, "win", CauseEligible)
	require.Empty(t, prop.Candidates, "winner itself is not a cancellation target")
	require.Len(t, prop.Runs, 1, "only the live self run is cancelled")
	require.Equal(t, CancelReasonSuperseded, reasonForRun(prop, "r_self_live"))
}

func TestPropagateTerminalStatesNeverKilled(t *testing.T) {
	st := ClusterState{
		Edges: []RelationEdge{edge("d", cluster.RelationDuplicateOf, "w")},
		Runs: []RunInfo{
			runInfo("r_ok", "d", StateSucceededForTest, 1),
			runInfo("r_failed", "d", "failed", 1),
			runInfo("r_cancelled", "d", "cancelled", 1),
			runInfo("r_timedout", "d", "timed_out", 1),
			runInfo("r_live", "d", StateQueued, 4),
		},
	}
	prop := PropagateCancellation(st, "w", CauseEligible)
	require.Len(t, prop.Runs, 1)
	require.Equal(t, "r_live", prop.Runs[0].RunID)
}

func TestPropagateDeterministicOrdering(t *testing.T) {
	mk := func() ClusterState {
		return ClusterState{
			Edges: []RelationEdge{
				edge("z", cluster.RelationDuplicateOf, "w"),
				edge("a", cluster.RelationDuplicateOf, "w"),
			},
			Runs: []RunInfo{
				runInfo("zz", "a", StateQueued, 1),
				runInfo("aa", "z", StateQueued, 2),
			},
		}
	}
	p1 := PropagateCancellation(mk(), "w", CauseEligible)
	p2 := PropagateCancellation(mk(), "w", CauseEligible)
	require.Equal(t, p1, p2)
	require.Equal(t, []string{"a", "z"}, idsOf(p1.Candidates))
	require.Equal(t, []string{"aa", "zz"}, []string{p1.Runs[0].RunID, p1.Runs[1].RunID})
}

func TestPropagateEmptyInputsSafe(t *testing.T) {
	prop := PropagateCancellation(ClusterState{}, "", CauseEligible)
	require.Empty(t, prop.Candidates)
	require.Empty(t, prop.Runs)
}

func idsOf(cands []CandidateCancellation) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.CandidateID
	}
	return out
}

func reasonForRun(p Propagation, runID string) string {
	for _, r := range p.Runs {
		if r.RunID == runID {
			return r.Reason
		}
	}
	return ""
}

const StateSucceededForTest = "succeeded"

// ActionForTest maps a propagation reason to its §2 action for assertions.
func ActionForTest(reason string) string {
	if reason == cluster.ReasonPrerequisiteFailed {
		return cluster.ActionBlock
	}
	return cluster.ActionSupersede
}
