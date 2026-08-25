package domain

import "fmt"

// EnvelopeKind discriminates the widened §4 push protocol
// (internal-protocols.md §4): one HMAC-gated door, three payload shapes.
// Absent kind decodes as KindDecision for v1 relay compatibility.
type EnvelopeKind string

// §4 envelope kinds accepted at POST /internal/connector/decisions.
const (
	KindDecision  EnvelopeKind = "decision"
	KindLifecycle EnvelopeKind = "lifecycle"
	KindRerun     EnvelopeKind = "rerun_requested"
)

// KindFor resolves the wire discriminator to a typed kind. An empty value
// maps to KindDecision so pre-widening control-plane relays keep flowing;
// anything unrecognized fails closed at the boundary.
func KindFor(raw string) (EnvelopeKind, error) {
	switch raw {
	case "":
		return KindDecision, nil
	case string(KindDecision):
		return KindDecision, nil
	case string(KindLifecycle):
		return KindLifecycle, nil
	case string(KindRerun):
		return KindRerun, nil
	default:
		return "", fmt.Errorf("unsupported envelope kind %q", raw)
	}
}

// CheckPhase is the check-run lifecycle position for one candidate revision.
// The connector walks ONE check run per revision queued → in_progress →
// completed (plan §4.1); completed never arrives as a push (it is produced
// by decision envelopes or the stalled sweeper).
type CheckPhase string

// Check phases as they surface on the GitHub check run.
const (
	PhaseQueued     CheckPhase = "queued"
	PhaseInProgress CheckPhase = "in_progress"
	PhaseCompleted  CheckPhase = "completed"
)

// GitHubStatus maps a phase to the Checks API status vocabulary.
func (p CheckPhase) GitHubStatus() string {
	switch p {
	case PhaseQueued:
		return "queued"
	case PhaseInProgress:
		return "in_progress"
	case PhaseCompleted:
		return "completed"
	default:
		// Callers validate before constructing; unreachable in practice,
		// but fail visible rather than silently normalizing.
		return "unknown_phase_" + string(p)
	}
}
