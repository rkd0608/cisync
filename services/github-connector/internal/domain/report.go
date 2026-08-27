package domain

import (
	"fmt"
	"time"
)

// Evidence verdicts carried on report dossier rows (§4.1 W6). Deferred rows
// are NOT positive evidence (I-01); renderers must never glyph them as ✓.
const (
	VerdictAccepted = "accepted"
	VerdictDeferred = "deferred"
	VerdictFailed   = "failed"
)

// ReportDossier is the OPTIONAL §4.1 W6 enrichment block the control-plane
// may push alongside a decision so the connector can render the sticky PR
// verification comment without scraping projections (same posture as the G6
// evidence counts). Every field is optional; sections degrade to muted
// placeholders when absent.
type ReportDossier struct {
	EvidenceRows []EvidenceRow       `json:"evidence_rows,omitempty"`
	Skipped      *SkippedNonEvidence `json:"skipped_non_evidence,omitempty"`
	Failures     []FailureCaseReport `json:"failures,omitempty"`
	Timeline     []TimelineEvent     `json:"timeline,omitempty"`
}

func (r *ReportDossier) validate() error {
	for i, row := range r.EvidenceRows {
		if err := row.validate(); err != nil {
			return fmt.Errorf("report.evidence_rows[%d]: %w", i, err)
		}
	}
	if r.Skipped != nil {
		if err := r.Skipped.validate(); err != nil {
			return fmt.Errorf("report.skipped_non_evidence: %w", err)
		}
	}
	for i, f := range r.Failures {
		if err := f.validate(); err != nil {
			return fmt.Errorf("report.failures[%d]: %w", i, err)
		}
	}
	for i, e := range r.Timeline {
		if err := e.validate(); err != nil {
			return fmt.Errorf("report.timeline[%d]: %w", i, err)
		}
	}
	return nil
}

// EvidenceRow is one verification kind's outcome: tier, verdict glyph source,
// executed/skipped census and wall-clock cost. Executed ≥ 0 and skipped ≥ 0
// separately — skipped-as-non-evidence rides skipped, never executed.
type EvidenceRow struct {
	Kind       string `json:"kind"`
	Tier       int    `json:"tier"`
	Verdict    string `json:"verdict"`
	Executed   int    `json:"executed"`
	Skipped    int    `json:"skipped"`
	DurationMS int64  `json:"duration_ms"`
}

func (e EvidenceRow) validate() error {
	if e.Kind == "" {
		return fmt.Errorf("kind required")
	}
	switch e.Verdict {
	case VerdictAccepted, VerdictDeferred, VerdictFailed:
	default:
		return fmt.Errorf("verdict must be accepted|deferred|failed, got %q", e.Verdict)
	}
	for name, v := range map[string]int{
		"tier": e.Tier, "executed": e.Executed, "skipped": e.Skipped,
	} {
		if v < 0 {
			return fmt.Errorf("%s must be >= 0, got %d", name, v)
		}
	}
	if e.DurationMS < 0 {
		return fmt.Errorf("duration_ms must be >= 0, got %d", e.DurationMS)
	}
	return nil
}

// SkippedNonEvidence explains WHY non-evidence was excluded — the WHY is
// mandatory whenever the block is pushed (plan T2/T4 calibrated language).
type SkippedNonEvidence struct {
	Total     int    `json:"total"`
	Rationale string `json:"rationale"`
}

func (s SkippedNonEvidence) validate() error {
	if s.Total < 0 {
		return fmt.Errorf("total must be >= 0, got %d", s.Total)
	}
	if s.Rationale == "" {
		return fmt.Errorf("rationale required")
	}
	return nil
}

// FailureCaseReport carries one failed-required-kind datum into the sticky
// comment's Failures & Repairs section (supersedes nothing: check-run
// annotations stay in place for GitHub-side surfaces).
type FailureCaseReport struct {
	Kind                string  `json:"kind"`
	Classification      string  `json:"classification"`
	Confidence          float64 `json:"confidence"`
	ReproductionCommand string  `json:"reproduction_command,omitempty"`
	RoutedAction        string  `json:"routed_action"`
	RepairAttempt       int     `json:"repair_attempt"`
	RepairMax           int     `json:"repair_max"`
}

func (f FailureCaseReport) validate() error {
	if f.Kind == "" || f.Classification == "" || f.RoutedAction == "" {
		return fmt.Errorf("kind, classification and routed_action are required")
	}
	if f.Confidence < 0 || f.Confidence > 1 {
		return fmt.Errorf("confidence must be within [0,1], got %.2f", f.Confidence)
	}
	if f.RepairAttempt < 0 || f.RepairMax < 0 || (f.RepairMax > 0 && f.RepairAttempt > f.RepairMax) {
		return fmt.Errorf("repair attempt/max inconsistent (%d/%d)", f.RepairAttempt, f.RepairMax)
	}
	return nil
}

// TimelineEvent is one compressed lifecycle line; control-plane pushes events
// in causal order and the renderer re-sorts defensively by At.
type TimelineEvent struct {
	At    time.Time `json:"at"`
	Event string    `json:"event"`
}

func (t TimelineEvent) validate() error {
	if t.At.IsZero() {
		return fmt.Errorf("at required")
	}
	if t.Event == "" {
		return fmt.Errorf("event required")
	}
	return nil
}
