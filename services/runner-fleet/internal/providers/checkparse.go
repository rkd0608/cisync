package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/redact"
)

// checkparse.go turns the sandbox's stdout stream into per-check results and
// the outcome census (P0-2). The protocol between the baked preset scripts
// and this parser is deliberately line-based so a plain docker log capture is
// sufficient — no side-channel files, no JSON slurp of the whole stream.
//
// Marker contract:
//
//	[cisync-check] {"tool":"eslint","verdict":"pass|fail|skip","duration_ms":N}
//	[cisync-tail] <tool>|<single-line human detail>
//
// WHY two lines: embedding free-form tool output into the check JSON would
// require shell-side JSON escaping (fragile across busybox/ash); keeping the
// tail on its own prefixed line lets Go own truncation + sanitization in ONE
// tested place.

// Verdict values used by both the scripts and the parser.
const (
	verdictPassed  = "passed"
	verdictFailed  = "failed"
	verdictSkipped = "skipped"

	checkMarkerPrefix = "[cisync-check] "
	tailMarkerPrefix  = "[cisync-tail] "
	detailSeparator   = "|"
	maxDetailChars    = 200 // brief: sanitized log tail capped at 200 chars
)

// CheckResult is one real executed-or-skipped validation step. It renders
// into the checks.json artifact; the census derived from it rides the same
// completion `results` field sim/docker already populate, so the evidence
// engine stays untouched.
type CheckResult struct {
	Tool       string `json:"tool"`
	Verdict    string `json:"verdict"`
	Executed   bool   `json:"executed"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail,omitempty"`
}

type checkMarker struct {
	Tool       string `json:"tool"`
	Verdict    string `json:"verdict"`
	DurationMS int64  `json:"duration_ms"`
}

// ParseCheckMarkers scans combined output for markers and tails and returns
// per-check results in emission order. Unknown verdicts fail closed: a value
// we cannot interpret must never be reported as a pass or even as evidence-
// carrying skip — it becomes an executed failure for the offending tool.
func ParseCheckMarkers(output string) []CheckResult {
	var checks []CheckResult
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, checkMarkerPrefix):
			var m checkMarker
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, checkMarkerPrefix)), &m); err != nil || m.Tool == "" {
				continue // malformed marker: raw output stays visible in logs
			}
			verdict := normalizedVerdict(m.Verdict)
			checks = append(checks, CheckResult{
				Tool:       m.Tool,
				DurationMS: m.DurationMS,
				// WHY skip ⇒ not-executed: the scripts emit `skip` only when
				// they refuse to run a tool (missing config/toolchain), so
				// counting it as executed would fake coverage.
				Executed: verdict != verdictSkipped,
				Verdict:  verdict,
			})
		case strings.HasPrefix(line, tailMarkerPrefix):
			payload := strings.TrimPrefix(line, tailMarkerPrefix)
			idx := strings.Index(payload, detailSeparator)
			if idx < 0 || len(checks) == 0 {
				continue
			}
			want := payload[:idx]
			last := &checks[len(checks)-1]
			if last.Tool != want {
				continue // stale tail for an unknown tool: never mis-attribute
			}
			if last.Detail == "" { // first tail wins; later lines append below
				last.Detail = sanitizeTail(payload[idx+1:])
			} else if len(last.Detail) < maxDetailChars {
				last.Detail = sanitizeTail(last.Detail + " " + payload[idx+1:])
			}
		}
	}
	return checks
}

func normalizedVerdict(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pass":
		return verdictPassed
	case "fail", "error":
		return verdictFailed
	case "skip":
		return verdictSkipped
	default:
		return verdictFailed
	}
}

// sanitizeTail runs the B3 secret scrubber then keeps at most maxDetailChars
// characters of the last content (failures usually print their reason last).
func sanitizeTail(s string) string {
	s = redact.String(strings.Join(strings.Fields(s), " "))
	const ellipsis = "…[tail]"
	runes := []rune(s)
	if len(runes) > maxDetailChars {
		s = ellipsis + string(runes[len(runes)-(maxDetailChars-len(ellipsis)):])
	}
	return s
}

// CensusFor derives the outcome census from parsed checks. Sum==Total always:
// each check lands in exactly one bucket, satisfying the complete-contract
// requirement that the results object sums to total when present.
func CensusFor(checks []CheckResult) domain.TestResults {
	census := domain.TestResults{Total: len(checks)}
	for _, c := range checks {
		switch c.Verdict {
		case verdictFailed:
			census.Failed++
		case verdictSkipped:
			census.Skipped++
		default:
			census.Passed++
		}
	}
	return census
}

// FallbackChecks hardens a NONZERO exit into an attributable failure. The
// normal failing-preset shape already carries an executed failure among its
// markers (a linter exits 1 after reporting errors) and must NOT gain a
// second synthetic one — otherwise partial failures double-count in the
// census. The fallback exists for harness-level deaths: nonzero exit with
// zero failures emitted anywhere (image bug, early OOM-kill) or all-passes-
// yet-nonzero mismatches, so a job can never be mistaken for clean evidence.
func FallbackChecks(exitCode int, existing []CheckResult) []CheckResult {
	out := append([]CheckResult(nil), existing...)
	explained := false
	for _, c := range out {
		if c.Executed && c.Verdict == verdictFailed {
			explained = true
		}
	}
	if explained {
		return out
	}
	return append(out, CheckResult{
		Tool:     "container",
		Verdict:  verdictFailed,
		Executed: true,
		Detail:   fmt.Sprintf("unattributed nonzero exit %d with no failing check", exitCode),
	})
}

func SkippedOnlyCheck(tool, reason string) CheckResult {
	return CheckResult{Tool: tool, Verdict: verdictSkipped, Detail: reason}
}

// ChecksArtifactName is the structured per-check artifact stored alongside
// logs.txt by every terminal realexec job.
const ChecksArtifactName = "checks.json"

func ChecksArtifact(checks []CheckResult) domain.Artifact {
	body, _ := json.MarshalIndent(checks, "", " ")
	return domain.Artifact{
		Name:        ChecksArtifactName,
		Digest:      DigestOf(body),
		SizeBytes:   int64(len(body)),
		Content:     body,
		ContentType: "application/json",
	}
}

// LogsArtifact stores the full job log pre-scrubbed through the B3 redactor;
// digest covers exactly the persisted bytes so downstream verification sees
// what was accepted.
func LogsArtifact(logs []byte) domain.Artifact {
	scrubbed := []byte(redact.String(string(logs)))
	return domain.Artifact{
		Name:        "logs.txt",
		Digest:      DigestOf(scrubbed),
		SizeBytes:   int64(len(scrubbed)),
		Content:     scrubbed,
		ContentType: "text/plain",
	}
}

// ExitCodeOf best-effort extracts an exit code from an exec error without
// importing os/exec's concrete type here.
func ExitCodeOf(err error) (int, bool) {
	type exitCoder interface{ ExitCode() int }
	if ec, ok := err.(exitCoder); ok {
		return ec.ExitCode(), true
	}
	return 0, false
}
