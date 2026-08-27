package checks

import (
	"fmt"
	"strings"
	"time"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// The summary strings below are BYTE-FROZEN contract surface (plan §4.3):
// branch protection dashboards and our own goldens key off them. Change only
// with a golden update + SPEC §3 row.

// headlineForVerb is the bold verdict line opener per decision verb.
func headlineForVerb(verb domain.DecisionVerb) (string, error) {
	switch verb {
	case domain.VerbEligibleForMergeTrain:
		return "Eligible for merge train", nil
	case domain.VerbRejected:
		return "Rejected", nil
	case domain.VerbDeferred:
		return "Deferred", nil
	default:
		return "", fmt.Errorf("checks: no headline mapping for verb %q", verb)
	}
}

// rfc3339 renders envelope timestamps deterministically (UTC, second
// precision) so dry-run goldens never depend on wall-clock.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// decisionSummary renders the completed-check markdown. W6 THIN CHECK:
// with evidence counts present it renders ONLY the verb+confidence+policy
// header, the evidence counts line and the dossier deep link — full
// intelligence moved to the sticky PR comment (internal/report). Without
// counts it falls back to the v1 flat format so pre-widening relays render
// unchanged.
func decisionSummary(d *domain.DecisionEnvelope, detailsURL string, cached bool) (string, error) {
	headline, err := headlineForVerb(d.Verb)
	if err != nil {
		return "", err
	}
	if d.Evidence == nil {
		// v1 legacy format preserved byte-for-byte for relay compatibility.
		return fmt.Sprintf("%s (verb=%s confidence=%.2f policy=%s v%d)",
			d.Summary, d.Verb, d.Confidence, d.Policy.PolicyID, d.Policy.Version), nil
	}
	e := d.Evidence
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** · confidence %.2f · policy %s v%d\n", headline, d.Confidence, d.Policy.PolicyID, d.Policy.Version)
	fmt.Fprintf(&b, "Evidence: %d/%d required accepted · %d deferred (reason-linked) · %d failed\n",
		e.Accepted, e.Required, e.Deferred, e.Failed)
	fmt.Fprintf(&b, "→ Full dossier: %s", detailsURL)
	if cached {
		fmt.Fprintf(&b, "\n_cached replay (no recompute)_")
	}
	return b.String(), nil
}

// lifecycleSummary renders queued/in_progress bodies (byte-frozen goldens).
func lifecycleSummary(e *domain.LifecycleEnvelope, detailsURL string, phase domain.CheckPhase) string {
	var b strings.Builder
	switch phase {
	case domain.PhaseQueued:
		fmt.Fprintf(&b, "**Queued** · CISync accepted candidate %s for verification\n", e.CandidateID)
	case domain.PhaseInProgress:
		fmt.Fprintf(&b, "**In progress** · CISync verification started for %s\n", e.CandidateID)
	}
	fmt.Fprintf(&b, "→ Full dossier: %s\n", detailsURL)
	switch phase {
	case domain.PhaseQueued:
		fmt.Fprintf(&b, "_queued %s_", rfc3339(e.At))
	case domain.PhaseInProgress:
		fmt.Fprintf(&b, "_in progress since %s_", rfc3339(e.At))
	}
	return b.String()
}

// stalledSummary flips an eternally-yellow required check to a visible neutral.
func stalledSummary(candidateID, detailsURL string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Stalled** · no verification result within the time budget\n")
	fmt.Fprintf(&b, "→ Full dossier: %s\n", detailsURL)
	fmt.Fprintf(&b, "_flipped neutral by stalled-check sweeper %s_", rfc3339(now))
	return b.String()
}

// rerunDeclineReason discriminates the two over-cap/unavailable neutral flips.
type rerunDeclineReason string

const (
	declineExhausted  rerunDeclineReason = "exhausted"
	declineUnavailabl rerunDeclineReason = "unavailable"
)

// rerunDeclinedSummary keeps mergers visibly informed instead of stranding a
// silently-ignored re-run (plan §4.5).
func rerunDeclinedSummary(candidateID, detailsURL string, now time.Time, reason rerunDeclineReason) string {
	var b strings.Builder
	switch reason {
	case declineExhausted:
		fmt.Fprintf(&b, "**Re-run budget exhausted** · cap reached for this candidate or hour\n")
	case declineUnavailabl:
		fmt.Fprintf(&b, "**Re-run unavailable** · control-plane unreachable; request not lost\n")
	}
	fmt.Fprintf(&b, "→ Full dossier: %s\n", detailsURL)
	if reason == declineExhausted {
		fmt.Fprintf(&b, "_re-run declined %s · see dossier for the standing verdict_", rfc3339(now))
	} else {
		fmt.Fprintf(&b, "_re-run declined %s · retry the GitHub re-run later_", rfc3339(now))
	}
	return b.String()
}

// overflowMessage is the frozen final-annotation text when findings exceed
// the 50-per-batch GitHub hard limit (plan §4.4).
func overflowMessage(hidden int) string {
	return fmt.Sprintf("%d more failing findings in the dossier", hidden)
}
