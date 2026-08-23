package failure

import (
	"regexp"
	"sort"
	"strings"
)

// Taxonomy classes (§1.7), values matching domain.Classification.
const (
	ClassInfraTransient          = "infra_transient"
	ClassKnownFlake              = "known_flake"
	ClassProbableFlake           = "probable_flake"
	ClassCompileRegression       = "compile_regression"
	ClassTestExpectationDrift    = "test_expectation_drift"
	ClassFunctionalRegression    = "functional_regression"
	ClassMergeConflict           = "merge_conflict"
	ClassSecurityPolicyViolation = "security_policy_violation"
	ClassTimeout                 = "timeout"
)

// Flake corroboration thresholds (EC-039: flake classes require forensics
// corroboration across independent reruns and environments).
const (
	CorroborationReruns       = 2
	CorroborationEnvironments = 2
)

// Confidence floor below which policy escalates
// (autonomy.escalate_on classification_confidence_lt_0.8).
const EscalationConfidenceFloor = 0.8

type rule struct {
	id    string
	re    *regexp.Regexp
	class string
	conf  float64
}

var rules = []rule{
	{"secret_or_policy_hit", regexp.MustCompile(`(?i)(secret|token|password|credential)[^\n]{0,80}(leak|expos|detect)|policy violation|egress (denied|blocked)|disallowed (path|host)`), ClassSecurityPolicyViolation, 0.99},
	{"merge_marker", regexp.MustCompile(`(?i)CONFLICT \(|^<{7} |Automatic merge failed`), ClassMergeConflict, 0.97},
	{"compile_error", regexp.MustCompile(`(?i)(compile|build) (error|failed)|undefined: |cannot find (package|module|symbol)|syntax error|does not compile|unresolved reference|declaration mismatch`), ClassCompileRegression, 0.93},
	{"golden_mismatch", regexp.MustCompile(`(?i)snapshot (mismatch|differs)|golden( file)? (mismatch|outdated|differs)|expectation drift`), ClassTestExpectationDrift, 0.85},
	{"infra_failure", regexp.MustCompile(`(?i)connection refused|connection reset|ECONNRESET|503 service unavailable|rate limit exceeded|too many requests|out of memory|\bOOM\b|signal: killed|exit status 137|no space left|i/o timeout|connection timed out|temporary failure in name resolution|runner heartbeat lost`), ClassInfraTransient, 0.9},
	{"budget_deadline", regexp.MustCompile(`(?i)(deadline|context deadline) exceeded|timed out after|time limit (of )?\d+|test timed out|budget exceeded`), ClassTimeout, 0.9},
	{"flaky_signal", regexp.MustCompile(`(?i)\bflaky\b|race (condition|detector) detected|non-deterministic|intermittent failure|passed on (retry|rerun)|failed then passed`), "", 0}, // class resolved by corroboration branch
	{"assertion_failure", regexp.MustCompile(`--- FAIL: |(?i)assertion failed|expect\w*\(.*\).*failed|verification failed|panic: `), ClassFunctionalRegression, 0.9},
}

// Context supplies the forensics side-channel for classification.
type Context struct {
	KnownFlakes          []string // test names or signature digests already quarantined
	CorroboratedReruns   int      // controlled reruns performed
	DistinctEnvironments int      // distinct environment fingerprints that reproduced
}

// FailureCase is the classifier's output payload; identity fields (fc_ id,
// run, candidate, state) are stamped by the caller.
type FailureCase struct {
	SignatureDigest          string
	Classification           string
	ClassificationConfidence float64
	RuleID                   string
	ReproductionCommand      string
	CausalSignals            []string
	SuspectedPaths           []string
}

// Classify assigns a taxonomy class to one failed run log.
//
// Rules evaluate in fixed order and every matching rule id is recorded as a
// causal signal; the first decisive rule sets the class. The flake rule is
// special: known flakes require the test/signature to appear in KnownFlakes,
// probable flakes require EC-039 corroboration (≥2 reruns across ≥2 distinct
// environments). Uncorroborated flaky signals yield probable_flake with
// sub-threshold confidence so autonomy escalates instead of auto-routing.
// Logs matching nothing classify as functional_regression at confidence 0.30
// (the enum has no "unknown"; sub-0.8 confidence forces human escalation).
func Classify(logText string, ctx Context) FailureCase {
	fc := FailureCase{
		SignatureDigest: SignatureDigest(logText),
	}
	var decisive rule
	matchedAny := false
	for _, r := range rules {
		if !r.re.MatchString(logText) {
			continue
		}
		fc.CausalSignals = append(fc.CausalSignals, r.id)
		if decisive.id == "" && !matchedAny {
			if r.class == "" {
				cls, conf := flakeBranch(fc.SignatureDigest, logText, ctx)
				fc.Classification = cls
				fc.ClassificationConfidence = conf
				decisive = r
			} else {
				decisive = r
				fc.Classification = r.class
				fc.ClassificationConfidence = r.conf
			}
		}
		matchedAny = true
	}
	if decisive.id == "" {
		fc.RuleID = "unclassified_fallback"
		fc.Classification = ClassFunctionalRegression
		fc.ClassificationConfidence = 0.30
	} else {
		fc.RuleID = decisive.id
	}
	fc.ReproductionCommand = ReproductionCommand(logText)
	fc.SuspectedPaths = SuspectedPaths(logText)
	return fc
}

func flakeBranch(digest, logText string, ctx Context) (string, float64) {
	testName := ExtractFailingTest(logText)
	for _, k := range ctx.KnownFlakes {
		if k == digest || (testName != "" && k == testName) {
			return ClassKnownFlake, 0.95
		}
	}
	if ctx.CorroboratedReruns >= CorroborationReruns && ctx.DistinctEnvironments >= CorroborationEnvironments {
		return ClassProbableFlake, 0.85
	}
	return ClassProbableFlake, 0.55
}

// ExtractFailingTest pulls the first "--- FAIL: <Test>" name from a log;
// empty when none present. Exported for repair tooling and tests.
func ExtractFailingTest(logText string) string {
	m := failingTestRe.FindStringSubmatch(logText)
	if m == nil {
		return ""
	}
	return m[1]
}

var failingTestRe = regexp.MustCompile(`--- FAIL: ([A-Za-z_][A-Za-z0-9_]*)`)

// SuspectedPaths extracts source file paths mentioned in the log, deduped,
// sorted, capped at 10.
func SuspectedPaths(logText string) []string {
	matches := pathRe.FindAllString(logText, -1)
	set := make(map[string]struct{}, len(matches))
	for _, p := range matches {
		if strings.Contains(p, "http") {
			continue
		}
		set[p] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

var pathRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_\-./]*\.(?:go|ts|tsx|js|mjs|py|java|kt|rb|rs|sql|yaml|yml|json|proto)`)
