package failure

import "regexp"

var (
	goPkgFailRe = regexp.MustCompile(`(?m)^FAIL[ \t]+([A-Za-z0-9_.\-/~]+)[ \t]`)
	shellLineRe = regexp.MustCompile(`(?m)^\$ (.+)$`)
)

// ReproductionCommand derives a deterministic reproduction command from the
// log: a go test invocation when both the failing test and its package are
// present; otherwise the first captured "$ <cmd>" line from captured runner
// output; otherwise empty (caller falls back to replaying the run).
func ReproductionCommand(logText string) string {
	test := ExtractFailingTest(logText)
	if m := goPkgFailRe.FindStringSubmatch(logText); m != nil && test != "" {
		return "go test " + m[1] + " -run '^" + test + "$' -count=1"
	}
	if m := shellLineRe.FindStringSubmatch(logText); m != nil {
		return m[1]
	}
	return ""
}
