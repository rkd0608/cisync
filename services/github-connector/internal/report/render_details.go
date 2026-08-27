package report

import (
	"fmt"
	"sort"
	"strings"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// verdictGlyph maps dossier verdicts to plan §3.1 glyphs: ✓ accepted, ○
// deferred (NEVER ✓ — skipped ≠ passed per T2/I-01), ✗ failed.
func verdictGlyph(verdict string) string {
	switch verdict {
	case domain.VerdictAccepted:
		return "✓"
	case domain.VerdictDeferred:
		return "○"
	default:
		return "✗"
	}
}

// writeEvidenceTable emits the markdown evidence table; rows render in pushed
// order (control-plane owns the presentation sequence).
func writeEvidenceTable(b *strings.Builder, rows []domain.EvidenceRow) {
	b.WriteString("| kind | tier | verdict | executed | skipped | duration |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %d | %s %s | %d | %d | %s |\n",
			r.Kind, r.Tier, verdictGlyph(r.Verdict), r.Verdict,
			r.Executed, r.Skipped, formatDuration(r.DurationMS))
	}
}

// failuresSection renders one numbered block per failed-required kind with
// classification, calibrated confidence, routed action, repair envelope and
// the reproduction command in a fenced bash block for one-click copy.
func failuresSection(b *strings.Builder, r *domain.ReportDossier) {
	b.WriteString("### Failures & Repairs\n")
	if r == nil || len(r.Failures) == 0 {
		b.WriteString("No failed-required evidence kinds.\n\n")
		return
	}
	for i, f := range r.Failures {
		number := i + 1
		if f.RepairMax > 0 {
			fmt.Fprintf(b, "%d. **%s — %s** (confidence %.2f, %s) · routed: `%s` · repair attempt %d/%d\n",
				number, f.Kind, f.Classification, f.Confidence,
				confidenceWord(f.Confidence), f.RoutedAction, f.RepairAttempt, f.RepairMax)
		} else {
			fmt.Fprintf(b, "%d. **%s — %s** (confidence %.2f, %s) · routed: `%s` · no repair attempts spent\n",
				number, f.Kind, f.Classification, f.Confidence,
				confidenceWord(f.Confidence), f.RoutedAction)
		}
		writeReproBlock(b, f.ReproductionCommand)
	}
	b.WriteString("\n")
}

// writeReproBlock fences the repro command; a blank command is information
// too (no path to reproduce), stated as such rather than an empty fence.
func writeReproBlock(b *strings.Builder, command string) {
	if command == "" {
		b.WriteString("   _No reproduction command provided._\n")
		return
	}
	fmt.Fprintf(b, "   ```bash\n   %s\n   ```\n", strings.ReplaceAll(command, "\n", " "))
}

// timelineSection compresses the lifecycle into seq-ordered bullets. Events
// are re-sorted by timestamp defensively so out-of-order pushes still read
// causally; stability preserves the control-plane's intra-second ordering.
func timelineSection(b *strings.Builder, r *domain.ReportDossier) {
	b.WriteString("### Decision timeline\n")
	if r == nil || len(r.Timeline) == 0 {
		b.WriteString("_timeline not pushed by control-plane._\n\n")
		return
	}
	events := make([]domain.TimelineEvent, len(r.Timeline))
	copy(events, r.Timeline)
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	for _, e := range events {
		fmt.Fprintf(b, "- `%s` %s\n", e.At.UTC().Format("2006-01-02T15:04:05Z"), e.Event)
	}
	b.WriteString("\n")
}

// formatDuration renders pushed milliseconds compactly: sub-second stays in
// ms ("800ms"), everything else rounds to whole seconds ("61s").
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%ds", ms/1000)
}
