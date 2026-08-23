package domain

import (
	"fmt"
	"time"
)

// CandidateState is the lifecycle state of a candidate.
type CandidateState string

// Candidate states (DOMAIN_MODEL_DRAFT §1.2).
const (
	CandSubmitted             CandidateState = "submitted"
	CandPlanned               CandidateState = "planned"
	CandValidating            CandidateState = "validating"
	CandRepairing             CandidateState = "repairing"
	CandEligible              CandidateState = "eligible"
	CandRejected              CandidateState = "rejected"
	CandBlockedRepresentative CandidateState = "blocked_representative"
	CandSuperseded            CandidateState = "superseded"
	CandCancelled             CandidateState = "cancelled"
)

var candidateTerminalStates = map[CandidateState]bool{
	CandEligible:   true,
	CandRejected:   true,
	CandSuperseded: true,
	CandCancelled:  true,
}

// Terminal reports whether the state is terminal (I-08).
func (s CandidateState) Terminal() bool { return candidateTerminalStates[s] }

// Relation enumerates candidate-to-representative relations.
type Relation string

// Relations (CandidateSummary.relation_to_rep enum plus representative).
const (
	RelDuplicate      Relation = "duplicate_of"
	RelAlternative    Relation = "alternative_of"
	RelComposable     Relation = "composable_with"
	RelConflicting    Relation = "conflicts_with"
	RelPrerequisite   Relation = "prerequisite_of"
	RelRepresentative Relation = "representative"
)

// Candidate is one concrete patch registered against an intent.
type Candidate struct {
	ID                string
	TenantID          string
	IntentID          string
	State             CandidateState
	Submitter         string
	PatchRef          string
	HeadSHA           string
	BaseSHA           string
	ChangedPaths      []string
	EstCostMillicents int64
	PriorityScore     float64
	ClusterID         string
	RelationToRep     *Relation
	CreatedAt         time.Time
}

// NewCandidate constructs a candidate in the submitted state after checking
// head_sha != base_sha.
func NewCandidate(id, tenantID, intentID, submitter, patchRef, headSHA, baseSHA string, changedPaths []string, estCost int64, now time.Time) (*Candidate, error) {
	if len(headSHA) != 40 || len(baseSHA) != 40 {
		return nil, fmt.Errorf("%w: head_sha/base_sha must be 40 hex chars", ErrValidationFailed)
	}
	if headSHA == baseSHA {
		return nil, fmt.Errorf("%w: head_sha must differ from base_sha", ErrValidationFailed)
	}
	return &Candidate{
		ID: id, TenantID: tenantID, IntentID: intentID, State: CandSubmitted,
		Submitter: submitter, PatchRef: patchRef, HeadSHA: headSHA, BaseSHA: baseSHA,
		ChangedPaths: changedPaths, EstCostMillicents: estCost, CreatedAt: now,
	}, nil
}

var candidateTransitions = map[string]transitionRule{
	"validation.planned":    {from: []string{string(CandSubmitted)}, to: string(CandPlanned)},
	"validation.admitted":   {from: []string{string(CandPlanned)}, to: string(CandValidating)},
	"repair.authorized":     {from: []string{string(CandValidating)}, to: string(CandRepairing)},
	"candidate.resubmitted": {from: []string{string(CandRepairing)}, to: string(CandValidating)},
	"decision.eligible":     {from: []string{string(CandValidating)}, to: string(CandEligible)},
	"decision.rejected":     {from: []string{string(CandValidating), string(CandPlanned)}, to: string(CandRejected)},
	"cluster.blocked":       {from: []string{string(CandPlanned), string(CandValidating)}, to: string(CandBlockedRepresentative)},
	"candidate.superseded":  {from: []string{string(CandSubmitted), string(CandPlanned), string(CandValidating), string(CandRepairing), string(CandBlockedRepresentative)}, to: string(CandSuperseded)},
	"candidate.cancelled":   {from: []string{string(CandSubmitted), string(CandPlanned), string(CandValidating), string(CandRepairing), string(CandBlockedRepresentative)}, to: string(CandCancelled)},
}

// Apply advances the candidate's state machine on the named trigger.
// Terminal aggregates log-and-ignore every further event (I-08).
func (c *Candidate) Apply(trigger string) error {
	if c.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := candidateTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for candidate", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(c.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, c.ID, c.State, trigger)
	}
	c.State = CandidateState(rule.to)
	return nil
}
