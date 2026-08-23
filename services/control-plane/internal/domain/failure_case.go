package domain

import (
	"fmt"
	"time"
)

// FailureState is the lifecycle state of a failure case.
type FailureState string

// FailureCase states (DOMAIN_MODEL_DRAFT §1.7).
const (
	FailureOpen       FailureState = "open"
	FailureClassified FailureState = "classified"
	FailureRouted     FailureState = "routed"
	FailureClosed     FailureState = "closed"
)

var failureTerminalStates = map[FailureState]bool{FailureClosed: true}

// Terminal reports whether the state is terminal (I-08).
func (s FailureState) Terminal() bool { return failureTerminalStates[s] }

// Classification is the failure taxonomy class.
type Classification string

// Failure taxonomy classes.
const (
	ClassInfraTransient          Classification = "infra_transient"
	ClassKnownFlake              Classification = "known_flake"
	ClassProbableFlake           Classification = "probable_flake"
	ClassCompileRegression       Classification = "compile_regression"
	ClassTestExpectationDrift    Classification = "test_expectation_drift"
	ClassFunctionalRegression    Classification = "functional_regression"
	ClassMergeConflict           Classification = "merge_conflict"
	ClassSecurityPolicyViolation Classification = "security_policy_violation"
	ClassTimeout                 Classification = "timeout"
)

// RoutedAction enumerates post-classification routing decisions.
type RoutedAction string

// Routed actions.
const (
	RouteRetry           RoutedAction = "retry"
	RouteQuarantineFlake RoutedAction = "quarantine_flake"
	RouteRepair          RoutedAction = "repair"
	RouteEscalateHuman   RoutedAction = "escalate_human"
	RouteReject          RoutedAction = "reject"
	RouteNone            RoutedAction = "none"
)

// FailureCase captures one attributable validation failure.
type FailureCase struct {
	ID                       string
	TenantID                 string
	CandidateID              string
	RunID                    string
	State                    FailureState
	SignatureDigest          string
	Classification           *Classification
	ClassificationConfidence float64
	ReproductionCommand      string
	SuspectedPaths           []string
	RoutedAction             RoutedAction
	CreatedAt                time.Time
}

// NewFailureCase constructs an open failure case.
func NewFailureCase(id, tenantID, candidateID, runID, signatureDigest, reproCmd string, suspectedPaths []string, now time.Time) *FailureCase {
	return &FailureCase{
		ID: id, TenantID: tenantID, CandidateID: candidateID, RunID: runID,
		State: FailureOpen, SignatureDigest: signatureDigest,
		ReproductionCommand: reproCmd, SuspectedPaths: suspectedPaths,
		RoutedAction: RouteNone, CreatedAt: now,
	}
}

var failureTransitions = map[string]transitionRule{
	"failure.classified": {from: []string{string(FailureOpen)}, to: string(FailureClassified)},
	"failure.routed":     {from: []string{string(FailureClassified)}, to: string(FailureRouted)},
	"failure.closed":     {from: []string{string(FailureRouted)}, to: string(FailureClosed)},
}

// Apply advances the failure case's state machine on the named trigger.
// Classification is immutable once set; corrections open a new case.
func (f *FailureCase) Apply(trigger string) error {
	if f.State.Terminal() {
		return ErrPostTerminal
	}
	rule, ok := failureTransitions[trigger]
	if !ok {
		return fmt.Errorf("%w: %s unknown trigger for failure_case", ErrUnknownEvent, trigger)
	}
	if !matchesState(rule.from, string(f.State)) {
		return fmt.Errorf("%w: %s in %s via %s", ErrIllegalTransition, f.ID, f.State, trigger)
	}
	f.State = FailureState(rule.to)
	return nil
}

// Classify stamps the immutable classification and confidence on an open case.
func (f *FailureCase) Classify(c Classification, confidence float64) error {
	if err := f.Apply("failure.classified"); err != nil {
		return err
	}
	f.Classification = &c
	f.ClassificationConfidence = confidence
	f.RoutedAction = routeForClass(c)
	return nil
}

func routeForClass(c Classification) RoutedAction {
	switch c {
	case ClassInfraTransient:
		return RouteRetry
	case ClassKnownFlake, ClassProbableFlake:
		return RouteQuarantineFlake
	case ClassCompileRegression, ClassMergeConflict, ClassFunctionalRegression:
		return RouteRepair
	case ClassSecurityPolicyViolation:
		return RouteEscalateHuman
	case ClassTimeout:
		return RouteRetry
	default:
		return RouteEscalateHuman
	}
}
