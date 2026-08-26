package scheduler

import (
	"testing"

	failurepkg "cisync.dev/cisync/control-plane/internal/failure"
)

// The unclassified fallback (no rule matched) carries confidence 0.30 — below
// the autonomy escalation floor. Routing it to "repair" parked candidates in
// repairing forever with nobody to apply the patch (v1 sim posture); the
// classifier contract says sub-floor classifications escalate to humans.
func TestRouteFailureEscalatesUnclassifiedFallback(t *testing.T) {
	// A log matching NO rule: neither assertion patterns nor infra signals.
	fc := failurepkg.Classify("step 1 ok\nstep 2 done\nexit code 1\n", failurepkg.Context{})
	if fc.RuleID != "unclassified_fallback" {
		t.Fatalf("fixture must hit the fallback, got rule %q (%s)", fc.RuleID, fc.Classification)
	}
	if got := routeFailure(fc); got != "escalate_human" {
		t.Fatalf("fallback must escalate, got %q", got)
	}
}

func TestRouteFailureStillRepairsConfidentRegressions(t *testing.T) {
	fc := failurepkg.Classify("--- FAIL: TestCheckoutTotals\nassertion failed\n", failurepkg.Context{})
	if fc.Classification != failurepkg.ClassFunctionalRegression || fc.ClassificationConfidence < failurepkg.EscalationConfidenceFloor {
		t.Fatalf("fixture must be a confident functional_regression, got %+v", fc)
	}
	if got := routeFailure(fc); got != "repair" {
		t.Fatalf("confident regression must repair, got %q", got)
	}
}

func TestRouteFailureRetriesTransient(t *testing.T) {
	fc := failurepkg.Classify("connection refused while contacting registry\n", failurepkg.Context{})
	if fc.Classification != failurepkg.ClassInfraTransient {
		t.Fatalf("fixture must be infra_transient, got %s", fc.Classification)
	}
	if got := routeFailure(fc); got != "retry" {
		t.Fatalf("transient must retry, got %q", got)
	}
}

func TestRouteFailureRejectsSecurityViolations(t *testing.T) {
	fc := failurepkg.Classify("policy violation: secret detected in diff\n", failurepkg.Context{})
	if got := routeFailure(fc); got != "reject" {
		t.Fatalf("security violation must reject, got %q", got)
	}
}
