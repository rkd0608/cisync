package domain

import (
	"fmt"
	"time"
)

// ClusterState is the lifecycle state of a duplicate/alternative cluster.
type ClusterState string

// Cluster states (DOMAIN_MODEL_DRAFT §1.3).
const (
	ClusterForming   ClusterState = "forming"
	ClusterActive    ClusterState = "active"
	ClusterDissolved ClusterState = "dissolved"
)

var clusterTerminalStates = map[ClusterState]bool{ClusterDissolved: true}

// Terminal reports whether the state is terminal (I-08).
func (s ClusterState) Terminal() bool { return clusterTerminalStates[s] }

// Cluster groups related candidates of one repo under a representative.
type Cluster struct {
	ID              string
	TenantID        string
	State           ClusterState
	Repo            string
	RepCandidateID  string
	MemberCount     int
	StrategyVersion string
	CreatedAt       time.Time
}

// NewCluster constructs a cluster in the forming state.
func NewCluster(id, tenantID, repo, repCandidateID, strategyVersion string, now time.Time) *Cluster {
	return &Cluster{
		ID: id, TenantID: tenantID, State: ClusterForming, Repo: repo,
		RepCandidateID: repCandidateID, MemberCount: 1,
		StrategyVersion: strategyVersion, CreatedAt: now,
	}
}

var clusterTransitions = map[string]transitionRule{
	"cluster.activated": {from: []string{string(ClusterForming)}, to: string(ClusterActive)},
	"cluster.dissolved": {from: []string{string(ClusterForming), string(ClusterActive)}, to: string(ClusterDissolved)},
}

// Apply advances the cluster's state machine on the named trigger.
// Terminal aggregates log-and-ignore every further event (I-08).
func (c *Cluster) Apply(trigger string) error {
	if c.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := clusterTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for cluster", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(c.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, c.ID, c.State, trigger)
	}
	c.State = ClusterState(rule.to)
	return nil
}

// ClusterMember is one candidate's membership edge inside a cluster.
type ClusterMember struct {
	CandidateID     string
	RelationToRep   Relation
	SimilarityScore float64
}
