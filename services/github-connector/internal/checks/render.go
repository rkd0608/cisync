// Package checks renders and publishes the "Agent Verification Gate" GitHub
// Check across its full lifecycle (queued → in_progress → completed) plus
// failure annotations, per plan §4 and internal-protocols §4.
package checks

import (
	"fmt"
	"time"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// CheckName is the stable check-run name agents and branch protection see.
const CheckName = "Agent Verification Gate"

// MaxAnnotationsPerBatch is the GitHub hard cap on annotations per API call;
// overflow collapses into one final "N more in dossier" annotation.
const MaxAnnotationsPerBatch = 50

// Annotation is the would-be check-run annotation in wire shape (mirrors the
// Checks API fields we set; dry-run logs show exactly this).
type Annotation struct {
	Path      string `json:"path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Message   string `json:"message"`
	Title     string `json:"title"` // carries the failed evidence kind
}

// CheckPayload is the would-be check run: identical shape in dry-run logs and
// live publishes so operators can diff exactly what would hit the API.
type CheckPayload struct {
	Name        string       `json:"name"`
	HeadSHA     string       `json:"head_sha"`
	Status      string       `json:"status"`
	Conclusion  string       `json:"conclusion,omitempty"` // omitted until completed
	DetailsURL  string       `json:"details_url"`
	ExternalID  string       `json:"external_id"`
	Summary     string       `json:"summary"`
	Annotations []Annotation `json:"annotations,omitempty"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
}

// detailsURLFor deep-links the EXISTING dossier page (plan §4.3).
func detailsURLFor(base, candidateID string) string {
	if base == "" {
		return "/candidates/" + candidateID
	}
	trimmed := base
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed + "/candidates/" + candidateID
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
		return "", &UnsupportedVerbError{Verb: string(verb)}
	}
}

// UnsupportedVerbError is the typed fail-closed error for unknown verbs.
type UnsupportedVerbError struct{ Verb string }

func (e *UnsupportedVerbError) Error() string {
	return "checks: no conclusion mapping for verb " + fmt.Sprintf("%q", e.Verb)
}

// RenderDecision maps a decision envelope onto a completed check-run payload.
// G6: external_id is now candidate_id so re-runs map back to the revision
// regardless of which decision the run carries. Annotations attach only to
// failure conclusions (G8). RenderedAt drives CompletedAt for byte-stability.
func RenderDecision(d *domain.DecisionEnvelope, detailsBase string) (CheckPayload, error) {
	conclusion, err := ConclusionForVerb(d.Verb)
	if err != nil {
		return CheckPayload{}, err
	}
	detailsURL := detailsURLFor(detailsBase, d.CandidateID)
	summary, err := decisionSummary(d, detailsURL, false)
	if err != nil {
		return CheckPayload{}, err
	}
	completed := d.RenderedAt.UTC()
	payload := CheckPayload{
		Name:        CheckName,
		HeadSHA:     d.HeadSHA,
		Status:      "completed",
		Conclusion:  conclusion,
		DetailsURL:  detailsURL,
		ExternalID:  d.CandidateID,
		Summary:     summary,
		CompletedAt: &completed,
	}
	if conclusion == "failure" {
		payload.Annotations = failureAnnotations(d.Annotations)
	}
	return payload, nil
}

// RenderCached republishes a stored decision with the frozen cached marker
// (rerun replay_cached policy; zero recompute).
func RenderCached(d *domain.DecisionEnvelope, detailsBase string) (CheckPayload, error) {
	conclusion, err := ConclusionForVerb(d.Verb)
	if err != nil {
		return CheckPayload{}, err
	}
	detailsURL := detailsURLFor(detailsBase, d.CandidateID)
	summary, err := decisionSummary(d, detailsURL, true)
	if err != nil {
		return CheckPayload{}, err
	}
	completed := d.RenderedAt.UTC()
	payload := CheckPayload{
		Name:        CheckName,
		HeadSHA:     d.HeadSHA,
		Status:      "completed",
		Conclusion:  conclusion,
		DetailsURL:  detailsURL,
		ExternalID:  d.CandidateID,
		Summary:     summary,
		CompletedAt: &completed,
	}
	if conclusion == "failure" {
		payload.Annotations = failureAnnotations(d.Annotations)
	}
	return payload, nil
}

// RenderLifecycle builds the queued/in_progress payload for an existing
// revision; callers publish it as create-or-update against the tracked run.
func RenderLifecycle(e *domain.LifecycleEnvelope, detailsBase string) (CheckPayload, error) {
	phase, err := e.Phase.GitHubPhase()
	if err != nil {
		return CheckPayload{}, err
	}
	detailsURL := detailsURLFor(detailsBase, e.CandidateID)
	return CheckPayload{
		Name:       CheckName,
		HeadSHA:    e.HeadSHA,
		Status:     phase.GitHubStatus(),
		DetailsURL: detailsURL,
		ExternalID: e.CandidateID,
		Summary:    lifecycleSummary(e, detailsURL, phase),
	}, nil
}

// RenderStalled is the sweeper safety net flipping eternal-yellow checks to
// neutral (plan §4.2).
func RenderStalled(candidateID, headSHA, detailsBase string, now time.Time) CheckPayload {
	detailsURL := detailsURLFor(detailsBase, candidateID)
	return CheckPayload{
		Name:        CheckName,
		HeadSHA:     headSHA,
		Status:      "completed",
		Conclusion:  "neutral",
		DetailsURL:  detailsURL,
		ExternalID:  candidateID,
		Summary:     stalledSummary(candidateID, detailsURL, now),
		CompletedAt: nowUTC(now),
	}
}

// RenderRerunDeclined renders the over-cap / ctrl-unreachable neutral flip
// so a required check never silently ignores a re-run (plan §4.5).
func RenderRerunDeclined(candidateID, headSHA, detailsBase string, now time.Time, exhausted bool) CheckPayload {
	detailsURL := detailsURLFor(detailsBase, candidateID)
	reason := declineUnavailabl
	if exhausted {
		reason = declineExhausted
	}
	return CheckPayload{
		Name:        CheckName,
		HeadSHA:     headSHA,
		Status:      "completed",
		Conclusion:  "neutral",
		DetailsURL:  detailsURL,
		ExternalID:  candidateID,
		Summary:     rerunDeclinedSummary(candidateID, detailsURL, now, reason),
		CompletedAt: nowUTC(now),
	}
}

// failureAnnotations caps findings at MaxAnnotationsPerBatch, collapsing
// overflow into the frozen final "N more" annotation.
func failureAnnotations(findings []domain.FindingAnnotation) []Annotation {
	if len(findings) == 0 {
		return nil
	}
	keep := findings
	overflow := 0
	if len(findings) > MaxAnnotationsPerBatch {
		overflow = len(findings) - (MaxAnnotationsPerBatch - 1)
		keep = findings[:MaxAnnotationsPerBatch-1]
	}
	out := make([]Annotation, 0, len(keep)+1)
	for _, f := range keep {
		out = append(out, Annotation{
			Path:      f.Path,
			StartLine: f.StartLine,
			EndLine:   f.StartLine,
			Message:   f.Message,
			Title:     f.Kind,
		})
	}
	if overflow > 0 {
		out = append(out, Annotation{Message: overflowMessage(overflow), Title: "dossier"})
	}
	return out
}

func nowUTC(now time.Time) *time.Time { t := now.UTC(); return &t }
