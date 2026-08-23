// Package checks renders and publishes one "Agent Verification Gate" GitHub
// Check per decision.rendered pushed by control-plane.
package checks

import (
	"fmt"
	"time"

	"sauron.dev/sauron/github-connector/internal/domain"
)

// CheckName is the stable check-run name agents and branch protection see.
const CheckName = "Agent Verification Gate"

// CheckPayload is the would-be check run: identical shape in dry-run logs and
// live publishes so operators can diff exactly what would hit the API.
type CheckPayload struct {
	Name       string    `json:"name"`
	HeadSHA    string    `json:"head_sha"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	DetailsURL string    `json:"details_url"`
	ExternalID string    `json:"external_id"`
	Summary    string    `json:"summary"`
	Completed  time.Time `json:"completed_at"`
}

// Render maps a decision envelope onto a completed check-run payload. The
// verb→conclusion mapping is the connector's single source of truth for how
// Sauron verdicts surface on GitHub:
//
//	eligible_for_merge_train → success
//	rejected                 → failure
//	deferred                 → neutral
func Render(d *domain.DecisionEnvelope, detailsURL string) (CheckPayload, error) {
	conclusion, err := ConclusionForVerb(d.Verb)
	if err != nil {
		return CheckPayload{}, err
	}
	return CheckPayload{
		Name:       CheckName,
		HeadSHA:    d.HeadSHA,
		Status:     "completed",
		Conclusion: conclusion,
		DetailsURL: detailsURL,
		ExternalID: d.DecisionID,
		Summary: fmt.Sprintf("%s (verb=%s confidence=%.2f policy=%s v%d)",
			d.Summary, d.Verb, d.Confidence, d.Policy.PolicyID, d.Policy.Version),
		Completed: time.Now().UTC(),
	}, nil
}

// ConclusionForVerb translates a decision verb into a GitHub check
// conclusion; unsupported verbs fail closed.
func ConclusionForVerb(verb domain.DecisionVerb) (string, error) {
	switch verb {
	case domain.VerbEligibleForMergeTrain:
		return "success", nil
	case domain.VerbRejected:
		return "failure", nil
	case domain.VerbDeferred:
		return "neutral", nil
	default:
		return "", fmt.Errorf("checks: no conclusion mapping for verb %q", verb)
	}
}
