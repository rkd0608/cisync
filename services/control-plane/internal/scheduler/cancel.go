package scheduler

import (
	"sort"

	"sauron.dev/sauron/control-plane/internal/cluster"
)

// Cause is the supersede-side trigger of a cancellation propagation.
type Cause string

// Causes (§2 relation table + §1.2 transitions).
const (
	CauseEligible  Cause = "eligible"  // winner rendered eligible
	CauseRejected  Cause = "rejected"  // winner terminal failure
	CauseCancelled Cause = "cancelled" // winner cancelled / intent closed
)

// Run lifecycle states, matching domain run states.
const (
	StateQueued     = "queued"
	StateDispatched = "dispatched"
	StateRunning    = "running"
)

// CancelReasonSuperseded is the reason attached to the superseded candidate's
// own live runs (validation.cancelled payload vocabulary).
const CancelReasonSuperseded = "superseded"

// RelationEdge is one directed candidate relation: From holds RelationTo To.
// Relation values match domain.Relation ("duplicate_of", …).
type RelationEdge struct {
	FromCandidateID string
	ToCandidateID   string
	Relation        string
}

// RunInfo is the run-level view needed for fence-aware cancellation.
type RunInfo struct {
	RunID       string
	CandidateID string
	State       string
	FenceToken  int64
}

// ClusterState is the global snapshot propagation runs against.
type ClusterState struct {
	Edges []RelationEdge
	Runs  []RunInfo
}

// CandidateCancellation cancels/blocks one candidate.
type CandidateCancellation struct {
	CandidateID string
	Reason      string // dominated_duplicate|tournament_loser|prerequisite_failed
}

// RunCancellation kills exactly one run. FenceToken echoes the token current
// at snapshot time: callers must verify it still matches before issuing the
// kill so a completed job or its successor on a fresh fence can never be hit
// by a stale cancel (EC-027 wrong-job kills).
type RunCancellation struct {
	RunID       string
	CandidateID string
	FenceToken  int64
	Reason      string // dominated_duplicate|tournament_loser|prerequisite_failed|superseded
}

// Propagation is the deterministic cancellation set.
type Propagation struct {
	Candidates []CandidateCancellation // sorted by candidate id; excludes the winner itself
	Runs       []RunCancellation       // sorted by run id; only non-terminal runs
}

// PropagateCancellation computes every run/candidate cancellation implied by
// one candidate reaching a terminal outcome:
//
//   - eligible: duplicate_of members are dominated (dominated_duplicate) and
//     alternative_of members lose the tournament (tournament_loser);
//   - rejected: prerequisite_of dependents are auto-blocked
//     (prerequisite_failed), never superseded — they may retarget;
//   - composable_with and conflicting_with edges never propagate;
//   - the winner's own queued/dispatched/running runs are cancelled with
//     reason "superseded".
//
// Terminal runs (succeeded/failed/timed_out/cancelled) are excluded: late
// results remain diagnostics and post-terminal cancels are ignored (I-08).
func PropagateCancellation(s ClusterState, supersededID string, cause Cause) Propagation {
	reasons := make(map[string]string)
	if supersededID != "" {
		reasons[supersededID] = CancelReasonSuperseded
	}

	edges := append([]RelationEdge(nil), s.Edges...)
	sortEdges(edges)
	for _, e := range edges {
		if e.ToCandidateID == "" || e.ToCandidateID != supersededID || e.FromCandidateID == "" {
			continue
		}
		switch {
		case e.Relation == cluster.RelationDuplicateOf && cause == CauseEligible:
			setReason(reasons, e.FromCandidateID, clusterReasonDominated)
		case e.Relation == cluster.RelationAlternativeOf && cause == CauseEligible:
			setReason(reasons, e.FromCandidateID, clusterReasonTournament)
		case e.Relation == cluster.RelationPrerequisite && cause == CauseRejected:
			setReason(reasons, e.FromCandidateID, clusterReasonPrereqFailed)
		}
	}

	var prop Propagation
	cands := make([]string, 0, len(reasons))
	for id := range reasons {
		cands = append(cands, id)
	}
	sortStrings(cands)
	for _, id := range cands {
		if id != supersededID {
			prop.Candidates = append(prop.Candidates, CandidateCancellation{CandidateID: id, Reason: reasons[id]})
		}
	}

	runs := append([]RunInfo(nil), s.Runs...)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	for _, r := range runs {
		reason, target := reasons[r.CandidateID]
		if !target {
			continue
		}
		if !isLive(r.State) {
			continue
		}
		prop.Runs = append(prop.Runs, RunCancellation{RunID: r.RunID, CandidateID: r.CandidateID, FenceToken: r.FenceToken, Reason: reason})
	}
	return prop
}

// Candidate-level reason values reuse cluster package vocabulary.
const (
	clusterReasonDominated    = cluster.ReasonDominatedDuplicate
	clusterReasonTournament   = cluster.ReasonTournamentLoser
	clusterReasonPrereqFailed = cluster.ReasonPrerequisiteFailed
)

func setReason(m map[string]string, id, reason string) {
	if cur, ok := m[id]; ok && id != "" && cur != CancelReasonSuperseded {
		// first non-self edge wins for determinism under sorted iteration
		return
	}
	m[id] = reason
}

func isLive(state string) bool {
	switch state {
	case StateQueued, StateDispatched, StateRunning:
		return true
	default:
		return false
	}
}

func sortEdges(e []RelationEdge) {
	sort.SliceStable(e, func(i, j int) bool {
		if e[i].FromCandidateID != e[j].FromCandidateID {
			return e[i].FromCandidateID < e[j].FromCandidateID
		}
		if e[i].ToCandidateID != e[j].ToCandidateID {
			return e[i].ToCandidateID < e[j].ToCandidateID
		}
		return e[i].Relation < e[j].Relation
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
