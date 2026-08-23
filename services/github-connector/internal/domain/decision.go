// Package decisions holds the decision envelope pushed by control-plane
// (internal-protocols.md §4).
package domain

import (
	"fmt"
	"strings"
	"time"
)

// DecisionVerb mirrors the v1 decision verbs (DOMAIN_MODEL_DRAFT §1.11).
type DecisionVerb string

// v1 decision verbs accepted on the connector boundary.
const (
	VerbEligibleForMergeTrain DecisionVerb = "eligible_for_merge_train"
	VerbRejected              DecisionVerb = "rejected"
	VerbDeferred              DecisionVerb = "deferred"
)

// PolicyRef is {policy_id, policy_version} per I-09.
type PolicyRef struct {
	PolicyID string `json:"policy_id"`
	Version  int    `json:"policy_version"`
}

// DecisionEnvelope is the control-plane → connector push payload. repo and
// head_sha enrich the raw decision.rendered event so the connector can place
// a GitHub Check without calling back into the API.
type DecisionEnvelope struct {
	DecisionID  string       `json:"decision_id"`
	CandidateID string       `json:"candidate_id"`
	Repo        string       `json:"repo"`
	HeadSHA     string       `json:"head_sha"`
	Verb        DecisionVerb `json:"verb"`
	Confidence  float64      `json:"confidence"`
	Policy      PolicyRef    `json:"policy"`
	Summary     string       `json:"summary"`
	RenderedAt  time.Time    `json:"rendered_at"`
}

// Validate enforces the boundary contract: fail early on anything that would
// produce an unplaceable or mislabeled check run.
func (d *DecisionEnvelope) Validate() error {
	if !strings.HasPrefix(d.DecisionID, "dec_") {
		return fmt.Errorf("decision_id must be a dec_-prefixed ULID, got %q", d.DecisionID)
	}
	if !strings.HasPrefix(d.CandidateID, "cand_") {
		return fmt.Errorf("candidate_id must be a cand_-prefixed ULID, got %q", d.CandidateID)
	}
	if d.Repo == "" || strings.Count(d.Repo, "/") != 1 {
		return fmt.Errorf("repo must be owner/name, got %q", d.Repo)
	}
	if len(d.HeadSHA) != 40 {
		return fmt.Errorf("head_sha must be 40 hex chars, got %q", d.HeadSHA)
	}
	switch d.Verb {
	case VerbEligibleForMergeTrain, VerbRejected, VerbDeferred:
	default:
		return fmt.Errorf("unsupported verb %q", d.Verb)
	}
	if d.Policy.PolicyID == "" || d.Policy.Version <= 0 {
		return fmt.Errorf("policy ref required (I-09)")
	}
	if d.RenderedAt.IsZero() {
		return fmt.Errorf("rendered_at required")
	}
	return nil
}
