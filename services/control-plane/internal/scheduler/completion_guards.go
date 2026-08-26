package scheduler

import (
	"cisync.dev/cisync/control-plane/internal/domain"
)

// This file encodes the decision-freshness rules for the completion
// ingestion path:
//   - a fleet completion may only drive effects while its run can legally
//     advance AND the owning candidate is still live (I-08: post-terminal
//     events are logged-and-ignored, never resurrect state);
//   - eligible_for_merge_train is forbidden while an unresolved failure of a
//     plan-required run or a pending repair exists — the verb must reflect
//     ALL completed evidence, so a required-kind failure forces
//     rejected/deferred/repair flow instead (never eligible).
//
// The pure predicates here are unit-tested in completion_guards_test.go;
// their wiring is covered by the PG-backed out-of-order regressions in
// completions_pg_test.go.

// completionIsDiagnostic reports whether a completion feed row must be
// absorbed as a diagnostic instead of driving effects. Absorption is required when:
//   - the run projection is not in a dispatchable state (queued means the
//     result cannot be real for this epoch; failed/timed_out/succeeded/
//     cancelled are already terminal or awaiting retry under a NEW fence,
//     which arrives as a different completion row); or
//   - the owning candidate has reached a terminal decision — late arrivals
//     are diagnostics only (I-08), e.g. a running job whose candidate was
//     superseded mid-flight.
func completionIsDiagnostic(runState domain.RunState, candidateState domain.CandidateState) bool {
	if candidateState.Terminal() {
		return true
	}
	switch runState {
	case domain.RunDispatched, domain.RunRunning:
		return false
	default:
		return true
	}
}

// eligibilityBlockReason returns why an eligible verdict is currently
// forbidden for the candidate ("" = allowed). Callers must treat any
// non-empty reason as "do not render eligible"; the failure router already
// owns deferred/rejected rendering at classification time, and D6 repair
// re-entry re-opens validation after the repair completes.
func eligibilityBlockReason(candidateState domain.CandidateState, failedRequiredRuns int) string {
	if candidateState.Terminal() {
		return fmtCandidateTerminal(candidateState)
	}
	if candidateState == domain.CandRepairing {
		return "repair pending; eligible forbidden until repair re-entry validates"
	}
	if failedRequiredRuns > 0 {
		return "plan-required run in unresolved failure; verb must never be eligible"
	}
	return ""
}

func fmtCandidateTerminal(state domain.CandidateState) string {
	return "candidate already terminal (" + string(state) + "); no further decisions"
}
