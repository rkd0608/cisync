package cluster

import "sort"

// RepEvent is the representative-side outcome that triggers supersede
// propagation across a cluster (§2 relation table).
type RepEvent string

// Representative events.
const (
	EventEligible  RepEvent = "eligible"
	EventRejected  RepEvent = "rejected"
	EventCancelled RepEvent = "cancelled"
)

// Supersede actions and reasons (§2 table and candidate.superseded payload).
const (
	ActionSupersede = "supersede"
	ActionBlock     = "block"

	ReasonDominatedDuplicate = "dominated_duplicate"
	ReasonTournamentLoser    = "tournament_loser"
	ReasonPrerequisiteFailed = "prerequisite_failed"
)

// SupersedeDecision is one member-level propagation outcome.
type SupersedeDecision struct {
	CandidateID string
	Action      string // supersede|block
	Reason      string
}

// SupersedeDecisions applies the §2 relation table to one cluster after a
// representative event:
//
//   - rep eligible: duplicate_of members are dominated (reason
//     dominated_duplicate); alternative_of members lose the bounded
//     tournament (reason tournament_loser).
//   - rep rejected: prerequisite_of members are auto-blocked, not
//     superseded — they may retarget (reason prerequisite_failed).
//
// composable_with and conflicting_with members are never touched; failed
// clusters dissolve and re-form instead of promoting duplicates (re-election
// is RESERVED until a later wave). Cancellation of a representative does not
// propagate to prerequisites: the §2 table scopes prerequisite_failed to
// rejection. Results are sorted by candidate ID for determinism.
func SupersedeDecisions(c ActiveCluster, event RepEvent) []SupersedeDecision {
	var out []SupersedeDecision
	for _, m := range c.Members {
		if m.Member.ID == c.Rep.ID {
			continue
		}
		switch event {
		case EventEligible:
			switch m.RelationToRep {
			case RelationDuplicateOf:
				out = append(out, SupersedeDecision{CandidateID: m.Member.ID, Action: ActionSupersede, Reason: ReasonDominatedDuplicate})
			case RelationAlternativeOf:
				out = append(out, SupersedeDecision{CandidateID: m.Member.ID, Action: ActionSupersede, Reason: ReasonTournamentLoser})
			}
		case EventRejected:
			if m.RelationToRep == RelationPrerequisite {
				out = append(out, SupersedeDecision{CandidateID: m.Member.ID, Action: ActionBlock, Reason: ReasonPrerequisiteFailed})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CandidateID < out[j].CandidateID })
	return out
}
