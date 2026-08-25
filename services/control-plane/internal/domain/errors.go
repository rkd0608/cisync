package domain

import "errors"

var (
	// ErrIllegalTransition reports an event that no legal transition accepts.
	ErrIllegalTransition = errors.New("illegal transition")
	// ErrPostTerminal reports an event delivered to an already-terminal
	// aggregate; callers must log-and-ignore it (invariant I-08).
	ErrPostTerminal = errors.New("post-terminal event ignored")
	// ErrUnknownEvent reports an event type absent from the aggregate's
	// transition table.
	ErrUnknownEvent = errors.New("unknown event")
	// ErrNotFound reports a missing aggregate within the caller's tenant.
	ErrNotFound = errors.New("not found")
	// ErrConflict reports a conflict_state rejection.
	ErrConflict = errors.New("conflict_state")
	// ErrLateSubmission reports a submission against a closed intent.
	ErrLateSubmission = errors.New("late_submission")
	// ErrDuplicateHead reports a live duplicate (intent_id, head_sha).
	ErrDuplicateHead = errors.New("duplicate_sha")
	// ErrLeaseNotRenewable reports renewal attempts on released/expired/
	// revoked leases.
	ErrLeaseNotRenewable = errors.New("lease not renewable")
	// ErrBudgetExceeded reports admission denial under exhausted budgets.
	ErrBudgetExceeded = errors.New("budget_exceeded")
	// ErrRateLimited reports token-bucket exhaustion at the edge.
	ErrRateLimited = errors.New("rate_limited")
	// ErrValidationFailed reports request-body schema violations.
	ErrValidationFailed = errors.New("validation_failed")
	// ErrUnauthorized reports missing or invalid bearer credentials.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrBadSignature reports HMAC verification failure on internal calls.
	ErrBadSignature = errors.New("bad signature")
	// ErrChainBroken reports hash-chain verification failure (fail-closed).
	ErrChainBroken = errors.New("ledger chain broken")
	// ErrCheckpointInvalid reports an Ed25519 checkpoint signature mismatch.
	ErrCheckpointInvalid = errors.New("checkpoint signature invalid")
	// ErrRerunBudgetExhausted reports a revalidate request beyond the
	// per-candidate rerun cap (wave-5 ruling #2).
	ErrRerunBudgetExhausted = errors.New("rerun_budget_exhausted")
)
