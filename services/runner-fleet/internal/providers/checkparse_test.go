package providers

import (
	"strings"
	"testing"
)

// TestParseCheckMarkers covers the envelope mapping from sandbox stdout onto
// per-check results and the outcome census. The real evidence honesty rules:
// executed failures must surface as failures, skips stay skips (never
// positive evidence), unknown verdicts fail closed.
func TestParseCheckMarkers(t *testing.T) {
	stdout := strings.Join([]string{
		"noise before markers",
		`[cisync-check] {"tool":"eslint","verdict":"pass","duration_ms":12}`,
		"eslint error: whatever",
		"[cisync-tail] eslint|e.g. token ghp_AbcdEFGH1234567890abcd secret=hunter2",
		`[cisync-check] {"tool":"tsc","verdict":"fail","duration_ms":40}`,
		"[cisync-tail] tsc|TS2304 Cannot find name 'x'",
		`[cisync-check] {"tool":"compileall","verdict":"skip","duration_ms":3}`,
		"[cisync-tail] compileall|no config",
		`[cisync-check] {"tool":"mystery","verdict":"banana","duration_ms":9}`,
	}, "\n")
	checks := ParseCheckMarkers(stdout)
	if len(checks) != 4 {
		t.Fatalf("expected 4 checks, got %d: %+v", len(checks), checks)
	}
	first := checks[0]
	if first.Tool != "eslint" || !first.Executed || first.Verdict != verdictPassed {
		t.Fatalf("pass marker must map to executed pass: %+v", first)
	}
	if first.DurationMS != 12 {
		t.Fatalf("duration must ride the marker, got %d", first.DurationMS)
	}
	// Sanitization: the tail goes through the B3 scrubber — no PAT or
	// kv-secret pattern may survive into stored detail.
	if first.Detail == "" || strings.Contains(first.Detail, "ghp_") {
		t.Fatalf("tail missing or unsanitized: %q", first.Detail)
	}
	if !strings.Contains(checks[1].Detail, "TS2304") {
		t.Fatalf("tail must be attributed to its tool: %+v", checks[1])
	}
	if checks[2].Verdict != verdictSkipped || checks[2].Executed {
		t.Fatalf("skip verdict must not count as executed: %+v", checks[2])
	}
	if checks[3].Verdict != verdictFailed {
		t.Fatalf("unknown verdict must fail closed, got %q", checks[3].Verdict)
	}

	census := CensusFor(checks)
	if census.Total != 4 || census.Passed != 1 || census.Failed != 2 || census.Skipped != 1 {
		t.Fatalf("census wrong: %+v", census)
	}
	if census.Sum() != census.Total {
		t.Fatalf("census must sum to total (I-01 contract): %+v", census)
	}
}

func TestTailTruncation(t *testing.T) {
	long := `[cisync-check] {"tool":"boom","verdict":"pass","duration_ms":1}` + "\n" +
		"[cisync-tail] boom|" + strings.Repeat("A", 500)
	checks := ParseCheckMarkers(long)
	if len(checks) != 1 {
		t.Fatalf("one marker expected, got %d", len(checks))
	}
	if len(checks[0].Detail) > 200+10 { // 200 content chars + ellipsis marker slack
		t.Fatalf("detail must be truncated to a 200-char tail, got %d chars", len(checks[0].Detail))
	}
}

// A container exiting nonzero without emitting any check marker means the
// harness died before/during checking (image bug, OOM-kill). Honesty rule:
// that is NOT a silent success — one fallback "container" failure carries the
// exit code so the job cannot be mistaken for executed-and-passed evidence.
func TestFallbackContainerCheck(t *testing.T) {
	checks := FallbackChecks(3, nil)
	if len(checks) != 1 || checks[0].Tool != "container" || checks[0].Verdict != verdictFailed || !checks[0].Executed {
		t.Fatalf("unexplained nonzero exit must append container failure: %+v", checks)
	}
	if !strings.Contains(checks[0].Detail, "3") {
		t.Fatalf("exit code must appear in detail: %q", checks[0].Detail)
	}
}

// Markers present + still-exiting-nonzero is the NORMAL failing-preset shape
// (a linter exits 1 after reporting its failures): no fallback is appended,
// otherwise partial failures would double-count in the census.
func TestNoFallbackWhenMarkersExplainExit(t *testing.T) {
	markers := `{"tool":"tsc","verdict":"fail","duration_ms":5}`
	checks := FallbackChecks(1, []CheckResult{{Tool: "tsc", Verdict: verdictFailed, Executed: true}})
	_ = markers
	if len(checks) != 1 {
		t.Fatalf("explained exit must keep the marker set as-is, got %+v", checks)
	}
}

// Skip-only runs MUST NOT look like passes when aggregated.
func TestSkippedOnlyCensusIsNotPositiveEvidence(t *testing.T) {
	checks := ParseCheckMarkers(
		`[cisync-check] {"tool":"eslint","verdict":"skip","duration_ms":1}` + "\n" +
			`[cisync-check] {"tool":"tsc","verdict":"skip","duration_ms":1}`)
	census := CensusFor(checks)
	if census.Total != 2 || census.Skipped != 2 || census.Passed != 0 || census.Failed != 0 {
		t.Fatalf("skips must stay skipped in census: %+v", census)
	}
}
