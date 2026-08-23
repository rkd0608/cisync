package planner

import "sauron.dev/sauron/control-plane/internal/policy"

// Fallback trigger identifiers. The first three reuse the §8 policy
// vocabulary ("fallback_triggers" array); the remaining four follow the same
// naming scheme for the triggers §3 lists as items 4–7.
const (
	FallbackUncertainty      = "uncertainty_gt_0.02"
	FallbackSparseHistory    = "sparse_history_lt_20"
	FallbackProtectedPaths   = "protected_paths"
	FallbackFlakeSignal      = "flake_signal_or_quarantine_14d"
	FallbackConflictCompose  = "conflicting_composition"
	FallbackAmbiguousRetry   = "ambiguous_failure_after_retry"
	FallbackExplicitOverride = "explicit_override"
)

// DefaultMinSamplesForSelection is the §3 sparse-history default: fewer
// observations than this for a touched surface class forces fallback.
const DefaultMinSamplesForSelection = 20

// AmbiguousFailureConfidenceThreshold is trigger 6's bound: failure class
// confidence below it after a bounded retry widens selection.
const AmbiguousFailureConfidenceThreshold = 0.8

// NoHistorySelectionConfidence is the sparse-data default from §4: with no
// learned-stats history, prediction uncertainty is maximal (confidence 0.5),
// which trips trigger 1 against the default min_selection_confidence of
// 0.98.
const NoHistorySelectionConfidence = 0.5

// evaluateFallbacks returns the fired fallback triggers in canonical §3
// order (1..7). Pure and deterministic; map iteration is avoided by walking
// surface classes in sorted order.
func evaluateFallbacks(in CandidateInput, body policy.PolicyBody) []string {
	var out []string

	// 1. prediction uncertainty above threshold.
	conf := effectiveSelectionConfidence(in)
	if conf < body.LadderOverrides.MinSelectionConfidence {
		out = append(out, FallbackUncertainty)
	}

	// 2. sparse history for any touched surface class.
	for _, class := range SurfaceClasses(in.ChangedPaths) {
		if in.SurfaceSamples[class] < DefaultMinSamplesForSelection {
			out = append(out, FallbackSparseHistory)
			break
		}
	}

	// 3. protected-path touch.
	for _, p := range body.LadderOverrides.ProtectedPaths {
		if matchAnyPath(p, in.ChangedPaths) {
			out = append(out, FallbackProtectedPaths)
			break
		}
	}

	// 4. active flake signal or recent quarantine within the selected set.
	if len(in.QuarantinedOrFlakeSignals) > 0 {
		out = append(out, FallbackFlakeSignal)
	}

	// 5. conflicting-relation composition or integration-set assembly.
	if in.ComposingIntegrationSet || in.RelationConflicting {
		out = append(out, FallbackConflictCompose)
	}

	// 6. ambiguous failure after bounded retry.
	if in.AmbiguousRetryFailureConfidence != nil &&
		*in.AmbiguousRetryFailureConfidence < AmbiguousFailureConfidenceThreshold {
		out = append(out, FallbackAmbiguousRetry)
	}

	// 7. explicit policy override / human request.
	if in.ExplicitFullSuiteOverride {
		out = append(out, FallbackExplicitOverride)
	}

	return out
}

func matchAnyPath(pattern string, paths []string) bool {
	for _, p := range paths {
		if MatchGlob(pattern, p) {
			return true
		}
	}
	return false
}
