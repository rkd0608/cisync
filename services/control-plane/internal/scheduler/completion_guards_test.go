package scheduler

import (
	"testing"

	"sauron.dev/sauron/control-plane/internal/domain"
)

/**
 * Regression guards for decision freshness (W3 close-out):
 *   1. completions arriving for terminal runs or decided candidates are
 *      absorbed as diagnostics (I-08) — never mutate state post-decision;
 *   2. eligible_for_merge_train is never rendered while a plan-required run
 *      sits in an unresolved failure or the candidate is parked in repair.
 */

func TestCompletionIsDiagnostic(t *testing.T) {
	live := []domain.RunState{domain.RunDispatched, domain.RunRunning}
	active := []domain.CandidateState{domain.CandSubmitted, domain.CandPlanned, domain.CandValidating, domain.CandRepairing}

	for _, runState := range live {
		for _, candState := range active {
			if completionIsDiagnostic(runState, candState) {
				t.Fatalf("run %s on active candidate %s must stay effectful", runState, candState)
			}
		}
	}

	terminalRuns := []domain.RunState{domain.RunSucceeded, domain.RunFailed, domain.RunTimedOut, domain.RunCancelled, domain.RunQueued}
	for _, runState := range terminalRuns {
		if !completionIsDiagnostic(runState, domain.CandValidating) {
			t.Fatalf("completion for %s run must be absorbed (no legal advance)", runState)
		}
	}

	terminalCandidates := []domain.CandidateState{domain.CandEligible, domain.CandRejected, domain.CandSuperseded, domain.CandCancelled}
	for _, candState := range terminalCandidates {
		for _, runState := range live {
			if !completionIsDiagnostic(runState, candState) {
				t.Fatalf("completion for live run %s on terminal candidate %s must be absorbed (I-08)", runState, candState)
			}
		}
	}
}

func TestEligibilityBlockReason(t *testing.T) {
	if reason := eligibilityBlockReason(domain.CandValidating, 0); reason != "" {
		t.Fatalf("clean validating candidate must be eligible-renderable, got blocker %q", reason)
	}
	if reason := eligibilityBlockReason(domain.CandRepairing, 0); reason == "" {
		t.Fatal("repairing candidate must never render eligible while repair is pending")
	}
	if reason := eligibilityBlockReason(domain.CandValidating, 2); reason == "" {
		t.Fatal("outstanding failed required-kind runs must block eligible")
	}
	for _, state := range []domain.CandidateState{domain.CandEligible, domain.CandSuperseded} {
		if reason := eligibilityBlockReason(state, 0); reason == "" {
			t.Fatalf("terminal candidate %s must never re-render", state)
		}
	}
}
