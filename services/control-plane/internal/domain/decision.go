package domain

import "time"

// DecisionVerb enumerates the v1 decision verbs.
type DecisionVerb string

// v1 decision verb subset (DOMAIN_MODEL_DRAFT §1.11).
const (
	VerbEligibleForMergeTrain DecisionVerb = "eligible_for_merge_train"
	VerbRejected              DecisionVerb = "rejected"
	VerbDeferred              DecisionVerb = "deferred"
)

// DecisionSubjectType enumerates decision subjects; IntegrationSet is
// reserved (D9).
type DecisionSubjectType string

// Decision subject types.
const (
	SubjectIntent         DecisionSubjectType = "intent"
	SubjectCandidate      DecisionSubjectType = "candidate"
	SubjectIntegrationSet DecisionSubjectType = "integration_set"
)

// ExplanationFactor is one named driver inside a decision explanation.
type ExplanationFactor struct {
	Name   string `json:"name"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// SkippedEvidence records a deliberately skipped evidence kind and why.
type SkippedEvidence struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// Explanation carries the human-readable justification of a decision.
type Explanation struct {
	Summary         string              `json:"summary"`
	Factors         []ExplanationFactor `json:"factors"`
	SkippedEvidence []SkippedEvidence   `json:"skipped_evidence,omitempty"`
}

// Decision is an immutable rendered fact stamped with its policy (I-09).
type Decision struct {
	ID           string
	TenantID     string
	SubjectType  DecisionSubjectType
	SubjectID    string
	Verb         DecisionVerb
	Confidence   float64
	Policy       PolicyRef
	Explanation  Explanation
	EvidenceRefs []string
	InputsHash   string
	CausationID  string
	RenderedAt   time.Time
}
