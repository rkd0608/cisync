// Package domain holds the widened control-plane → connector push envelopes
// (internal-protocols.md §4) and the boundary validation for each shape.
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

// EvidenceCounts are the dossier evidence census pushed WITH the decision
// (G6 contract delta): the connector renders them verbatim into the check
// summary instead of scraping projections. Exact wire names:
// {"required","accepted","deferred","failed"} — all non-negative ints.
type EvidenceCounts struct {
	Required int `json:"required"`
	Accepted int `json:"accepted"`
	Deferred int `json:"deferred"`
	Failed   int `json:"failed"`
}

func (e EvidenceCounts) validate() error {
	for name, v := range map[string]int{
		"required": e.Required, "accepted": e.Accepted,
		"deferred": e.Deferred, "failed": e.Failed,
	} {
		if v < 0 {
			return fmt.Errorf("evidence.%s must be >= 0, got %d", name, v)
		}
	}
	if e.Accepted > e.Required || e.Deferred+e.Failed > e.Required {
		return fmt.Errorf("evidence counts inconsistent: accepted/deferred/failed exceed required (%+v)", e)
	}
	return nil
}

// FindingAnnotation carries one failed-required-kind datum into the failure
// annotations (plan §4.4). path may be empty ⇒ file-level message annotation;
// start_line is omitted on the wire when zero. Message and kind are required.
type FindingAnnotation struct {
	Path      string `json:"path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	Message   string `json:"message"`
	Kind      string `json:"kind"`
}

func (a FindingAnnotation) validate() error {
	if a.Message == "" {
		return fmt.Errorf("annotation.message required")
	}
	if a.Kind == "" {
		return fmt.Errorf("annotation.kind required")
	}
	if a.StartLine < 0 {
		return fmt.Errorf("annotation.start_line must be >= 0")
	}
	if a.Path != "" && strings.HasPrefix(a.Path, "/") {
		return fmt.Errorf("annotation.path must be repo-relative, got %q", a.Path)
	}
	return nil
}

// DecisionEnvelope is the kind=decision §4 push. G6 deltas vs v1:
// external_id on the rendered check becomes candidate_id (NOT decision_id),
// and the optional evidence/annotations blocks widen the summary/annotations.
type DecisionEnvelope struct {
	Kind        EnvelopeKind        `json:"kind,omitempty"` // absent ⇒ decision (v1 compat)
	DecisionID  string              `json:"decision_id"`
	CandidateID string              `json:"candidate_id"`
	Repo        string              `json:"repo"`
	HeadSHA     string              `json:"head_sha"`
	Verb        DecisionVerb        `json:"verb"`
	Confidence  float64             `json:"confidence"`
	Policy      PolicyRef           `json:"policy"`
	Summary     string              `json:"summary"`
	RenderedAt  time.Time           `json:"rendered_at"`
	Evidence    *EvidenceCounts     `json:"evidence,omitempty"`
	Annotations []FindingAnnotation `json:"annotations,omitempty"`
}

// Validate enforces the boundary contract: fail early on anything that would
// produce an unplaceable or mislabeled check run.
func (d *DecisionEnvelope) Validate() error {
	if d.Kind != "" && d.Kind != KindDecision {
		return fmt.Errorf("decision envelope cannot carry kind %q", d.Kind)
	}
	if !strings.HasPrefix(d.DecisionID, "dec_") {
		return fmt.Errorf("decision_id must be a dec_-prefixed ULID, got %q", d.DecisionID)
	}
	if !strings.HasPrefix(d.CandidateID, "cand_") {
		return fmt.Errorf("candidate_id must be a cand_-prefixed ULID, got %q", d.CandidateID)
	}
	if err := validateRepoHead(d.Repo, d.HeadSHA); err != nil {
		return err
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
	if d.Evidence != nil {
		if err := d.Evidence.validate(); err != nil {
			return err
		}
	}
	for i, a := range d.Annotations {
		if err := a.validate(); err != nil {
			return fmt.Errorf("annotations[%d]: %w", i, err)
		}
	}
	return nil
}

// validateRepoHead is the shared owner/name + 40-hex head guard used by all
// envelope shapes; hex enforcement keeps malformed SHAs out of the Checks API.
func validateRepoHead(repo, headSHA string) error {
	if repo == "" || strings.Count(repo, "/") != 1 {
		return fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	if len(headSHA) != 40 || !isHex(headSHA) {
		return fmt.Errorf("head_sha must be 40 hex chars, got %q", headSHA)
	}
	return nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
