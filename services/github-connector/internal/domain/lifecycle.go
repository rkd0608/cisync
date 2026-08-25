package domain

import (
	"fmt"
	"time"
)

// LifecyclePhase is the phase carried by kind=lifecycle pushes: only the two
// non-terminal phases travel the wire; completion arrives as a decision.
type LifecyclePhase string

// Wire phases for lifecycle envelopes (plan §4.1).
const (
	LifecycleQueued     LifecyclePhase = "queued"
	LifecycleInProgress LifecyclePhase = "in_progress"
)

// GitHubPhase projects the wire phase onto the check-run phase vocabulary.
func (p LifecyclePhase) GitHubPhase() (CheckPhase, error) {
	switch p {
	case LifecycleQueued:
		return PhaseQueued, nil
	case LifecycleInProgress:
		return PhaseInProgress, nil
	default:
		return "", fmt.Errorf("unsupported lifecycle phase %q", p)
	}
}

// LifecycleEnvelope is the widened §4 lifecycle push emitted by the control
// plane outbox relay: candidate.submitted ⇒ queued, first validation.started
// per candidate ⇒ in_progress. The connector UPDATES the same check run so
// one revision walks queued → in_progress → completed in place.
//
// Idempotency-Key on the wire MUST equal "<candidate_id>:<phase>" — it is
// deterministic, so relay redeliveries collapse without connector-side state.
type LifecycleEnvelope struct {
	Kind        EnvelopeKind   `json:"kind"` // must be "lifecycle"
	Phase       LifecyclePhase `json:"phase"`
	CandidateID string         `json:"candidate_id"`
	Repo        string         `json:"repo"`
	HeadSHA     string         `json:"head_sha"`
	At          time.Time      `json:"at"` // stamped for byte-stable dry-run goldens
}

// Validate enforces the lifecycle boundary contract.
func (e *LifecycleEnvelope) Validate() error {
	if e.Kind != KindLifecycle {
		return fmt.Errorf("lifecycle envelope requires kind %q, got %q", KindLifecycle, e.Kind)
	}
	if _, err := e.Phase.GitHubPhase(); err != nil {
		return err
	}
	if !isCandID(e.CandidateID) {
		return fmt.Errorf("candidate_id must be a cand_-prefixed ULID, got %q", e.CandidateID)
	}
	if err := validateRepoHead(e.Repo, e.HeadSHA); err != nil {
		return err
	}
	if e.At.IsZero() {
		return fmt.Errorf("at required")
	}
	return nil
}

// RerunEnvelope relays a GitHub check_run.rerequested whose external_id
// matched one of our candidates (external_id == candidate_id since G6).
// Idempotency-Key on the wire is the originating GitHub ext_delivery_id.
type RerunEnvelope struct {
	Kind        EnvelopeKind `json:"kind"` // must be "rerun_requested"
	CandidateID string       `json:"candidate_id"`
	Repo        string       `json:"repo"`
	HeadSHA     string       `json:"head_sha"`
	RequestedBy string       `json:"requested_by,omitempty"` // display-only provenance (plan §2.2)
	RequestedAt time.Time    `json:"requested_at"`
}

// Validate enforces the rerun boundary contract.
func (e *RerunEnvelope) Validate() error {
	if e.Kind != KindRerun {
		return fmt.Errorf("rerun envelope requires kind %q, got %q", KindRerun, e.Kind)
	}
	if !isCandID(e.CandidateID) {
		return fmt.Errorf("candidate_id must be a cand_-prefixed ULID, got %q", e.CandidateID)
	}
	if err := validateRepoHead(e.Repo, e.HeadSHA); err != nil {
		return err
	}
	if e.RequestedAt.IsZero() {
		return fmt.Errorf("requested_at required")
	}
	return nil
}

func isCandID(id string) bool { return len(id) > len("cand_") && id[:len("cand_")] == "cand_" }
