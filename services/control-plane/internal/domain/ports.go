package domain

import "context"

// CandidateInput is the planner's view of a freshly submitted candidate.
type CandidateInput struct {
	CandidateID  string
	IntentID     string
	Repo         string
	BaseSHA      string
	HeadSHA      string
	PatchRef     string
	ChangedPaths []string
	RiskClass    RiskClass
}

// Planner builds the tiered ValidationPlan for an admitted candidate.
// Implementations must stamp ResolvedPolicy.Ref into the plan (I-09).
type Planner interface {
	Plan(ctx context.Context, cand CandidateInput, pol ResolvedPolicy) (*ValidationPlan, error)
}

// Scheduler admits and dispatches queued validation runs; Tick returns the
// number of runs dispatched during the call.
type Scheduler interface {
	Tick(ctx context.Context) (int, error)
}

// LeaseRequest describes a desired change-scope lease grant.
type LeaseRequest struct {
	TenantID         string
	IntentID         string
	Scope            LeaseScope
	Holder           string
	TTL              int64
	RequiredEvidence []string
	Policy           ResolvedPolicy
}

// LeaseGrant is the outcome of a successful lease authorization.
type LeaseGrant struct {
	Lease           *Lease
	AllowedPaths    []string
	ProhibitedPaths []string
	Conflicts       []ConflictRef
	QueuePosition   *int
	EtaSeconds      *int
}

// LeaseAuthorizer grants scoped leases under capacity + budget constraints
// (invariant I-06).
type LeaseAuthorizer interface {
	Authorize(ctx context.Context, req LeaseRequest) (*LeaseGrant, error)
}

// ProposedEvidence is an evidence record awaiting evaluation.
type ProposedEvidence struct {
	Record       *EvidenceRecord
	RunState     RunState
	FenceToken   int64
	CurrentFence int64
}

// EvidenceOutcome is the accept/reject ruling plus the reason on rejection.
type EvidenceOutcome struct {
	Accepted bool
	Reason   string
}

// EvidenceEvaluator applies I-01/I-02/I-03 provenance checks to proposed
// evidence before acceptance.
type EvidenceEvaluator interface {
	Evaluate(ctx context.Context, ev ProposedEvidence) (EvidenceOutcome, error)
}
