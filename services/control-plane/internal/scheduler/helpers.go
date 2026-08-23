package scheduler

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"sauron.dev/sauron/control-plane/internal/domain"
	evidencepkg "sauron.dev/sauron/control-plane/internal/evidence"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

func logf(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...))
}

var stderrLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// toAnySlice converts string slices into the JSON payload representation.
func toAnySlice(in []string) []any {
	if in == nil {
		return []any{}
	}
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// evidenceContext builds the engine acceptance context: the lease identity
// this completion was fenced under plus the plan-level reuse key (I-02).
func evidenceContext(plan *domain.ValidationPlan, leaseJTI string, prior []store.EvidenceRef) evidencepkg.Context {
	ctx := evidencepkg.Context{
		ExpectedLeaseJTI:   leaseJTI,
		ExpectedInputsHash: plan.InputsHash,
	}
	for _, ref := range prior {
		ctx.Accepted = append(ctx.Accepted, evidencepkg.AcceptedRef{
			RunID: "", Attempt: ref.Attempt, LeaseJTI: "",
		})
	}
	return ctx
}

// evidenceEvaluate adapts the pure evaluator to a record + context pair.
func evidenceEvaluate(rec *domain.EvidenceRecord, ctx evidencepkg.Context) evidencepkg.Evaluation {
	proposal := evidencepkg.ProposedRecord{
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
	case evidencepkg.VerdictPass:
		proposal.Outcome = evidencepkg.OutcomePassed
	default:
		proposal.Outcome = evidencepkg.OutcomeFailed
	}
	return evidencepkg.Evaluate(proposal, ctx)
}

// evidenceSufficiency maps accepted refs onto the D8 formula.
func evidenceSufficiency(requiredKinds []string, accepted []store.EvidenceRef) float64 {
	records := make([]evidencepkg.AcceptedRecord, 0, len(accepted))
	for _, ref := range accepted {
		records = append(records, evidencepkg.AcceptedRecord{Kind: ref.Kind, Verdict: "pass", Status: "accepted"})
	}
	return evidencepkg.Sufficiency(requiredKinds, records)
}

func timeNowUTC() time.Time { return time.Now().UTC() }

func relayEnqueueRequest(run *domain.ValidationRun) relay.EnqueueRequest {
	return relay.EnqueueRequest{
		RunID:   run.ID,
		Attempt: run.Attempt,
		Tier:    run.Tier,
		Pool:    run.Pool,
		JobSpec: jobSpecToMap(run.JobSpec),
	}
}
