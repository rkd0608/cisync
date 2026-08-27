// Package report renders and posts the sticky "CISync Verification Report"
// issue comment — one per (repo, pull_number), updated in place on every
// decision push so PR conversations carry the dossier intelligence
// CodeRabbit-style (W6; internal-protocols §4.1 + PRODUCT_UX_PLAN §3.3).
package report

import (
	"fmt"
	"strings"

	"cisync.dev/cisync/github-connector/internal/checks"
	"cisync.dev/cisync/github-connector/internal/domain"
)

// MarkerLine is the first line of every sticky comment. The poster finds a
// prior comment by this exact prefix (only OUR comments match) and PATCHes it.
const MarkerLine = "<!-- cisync:report -->"

// RenderComment renders the full sticky comment body for one decision. It is
// pure: no clock, no network, byte-stable UTC formatting so golden tests hold.
func RenderComment(env *domain.DecisionEnvelope, detailsBase string) (string, error) {
	headline, err := checks.HeadlineForVerb(env.Verb)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(MarkerLine + "\n")
	fmt.Fprintf(&b, "## CISync Verification Report\n\n")
	fmt.Fprintf(&b, "**%s** · confidence %.2f (%s) · policy %s v%d\n\n",
		headline, env.Confidence, confidenceWord(env.Confidence),
		env.Policy.PolicyID, env.Policy.Version)
	if env.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", env.Summary)
	}
	evidenceSection(&b, env)
	skippedSection(&b, env.Report)
	failuresSection(&b, env.Report)
	timelineSection(&b, env.Report)
	if err := dossierLink(&b, detailsBase, env.CandidateID); err != nil {
		return "", err
	}
	return b.String(), nil
}

// evidenceSection renders the per-kind table when pushed plus the aggregate
// census line (always present: the bridge between W5 counts and W6 rows).
func evidenceSection(b *strings.Builder, env *domain.DecisionEnvelope) {
	b.WriteString("### Evidence by kind\n")
	if env.Report != nil && len(env.Report.EvidenceRows) > 0 {
		writeEvidenceTable(b, env.Report.EvidenceRows)
	} else {
		b.WriteString("_per-kind breakdown not pushed by control-plane._\n")
	}
	if e := env.Evidence; e != nil {
		fmt.Fprintf(b, "\nAggregate census: %d required · %d accepted · %d deferred (reason-linked) · %d failed.\n\n",
			e.Required, e.Accepted, e.Deferred, e.Failed)
		return
	}
	b.WriteString("\n")
}

// skippedSection renders non-evidence totals WITH the mandatory WHY string;
// absence renders as an honest placeholder, never an empty section.
func skippedSection(b *strings.Builder, r *domain.ReportDossier) {
	b.WriteString("### Skipped as non-evidence\n")
	switch {
	case r != nil && r.Skipped != nil && r.Skipped.Rationale != "":
		fmt.Fprintf(b, "**%d** items skipped as non-evidence — %s.\n\n", r.Skipped.Total, r.Skipped.Rationale)
	default:
		b.WriteString("_skip rationale not pushed by control-plane._\n\n")
	}
}

// dossierLink pins the live candidate permalink footer (§3.2 deep link).
func dossierLink(b *strings.Builder, detailsBase, candidateID string) error {
	url := checks.CandidateDetailsURL(detailsBase, candidateID)
	fmt.Fprintf(b, "> Full dossier: %s — live link\n", url)
	return nil
}

// confidenceWord implements plan T4 calibrated language (words beside numbers).
func confidenceWord(c float64) string {
	switch {
	case c >= 0.95:
		return "high"
	case c >= 0.80:
		return "moderate"
	case c >= 0.50:
		return "low"
	default:
		return "insufficient"
	}
}
