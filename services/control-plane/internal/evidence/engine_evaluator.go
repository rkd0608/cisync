package evidence

import (
	"context"
	"fmt"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// EngineEvaluator adapts the pure provenance validator to the
// domain.EvidenceEvaluator port from the W1 contracts slice.
//
// The port carries only record + fence state, so callers supply an
// Expectations resolver that binds each proposal to its acceptance context
// (expected lease identity, plan inputs hash, prior accepted records).
type EngineEvaluator struct {
	Resolve Expectations
}

// Expectations resolves the acceptance Context for one proposal.
type Expectations func(ctx context.Context, rec *domain.EvidenceRecord) (Context, error)

// NewEngineEvaluator wires the adapter.
func NewEngineEvaluator(resolve Expectations) *EngineEvaluator {
	return &EngineEvaluator{Resolve: resolve}
}

// Evaluate implements domain.EvidenceEvaluator: it maps the port record onto
// the engine proposal shape and returns the accept/reject ruling. Fence
// mismatches reject before provenance checks (I-11 precedence).
func (e *EngineEvaluator) Evaluate(ctx context.Context, ev domain.ProposedEvidence) (domain.EvidenceOutcome, error) {
	if ev.Record == nil {
		return domain.EvidenceOutcome{}, fmt.Errorf("evidence adapter: nil record")
	}
	if ev.FenceToken != ev.CurrentFence {
		return domain.EvidenceOutcome{Accepted: false, Reason: "fence_mismatch"}, nil
	}
	if e.Resolve == nil {
		return domain.EvidenceOutcome{}, fmt.Errorf("evidence adapter: no expectations resolver wired")
	}
	acceptCtx, err := e.Resolve(ctx, ev.Record)
	if err != nil {
		return domain.EvidenceOutcome{}, err
	}
	ruling := Evaluate(proposalFromDomain(ev.Record), acceptCtx)
	return domain.EvidenceOutcome{
		Accepted: ruling.Action == ActionAccept,
		Reason:   ruling.Reason,
	}, nil
}

func proposalFromDomain(rec *domain.EvidenceRecord) ProposedRecord {
	p := ProposedRecord{
		ID:          rec.ID,
		RunID:       rec.RunID,
		CandidateID: rec.CandidateID,
		Kind:        rec.Kind,
		Verdict:     rec.Verdict,
		Digests:     rec.Digests,
		InputsHash:  rec.InputsHash,
		LeaseJTI:    rec.ProducedByLease,
		Attempt:     rec.Attempt,
	}
	switch rec.Verdict {
	case VerdictPass:
		p.Outcome = OutcomePassed
	default:
		p.Outcome = OutcomeFailed
	}
	return p
}

var _ domain.EvidenceEvaluator = (*EngineEvaluator)(nil)
