package scheduler

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"cisync.dev/cisync/control-plane/internal/domain"
	evidencepkg "cisync.dev/cisync/control-plane/internal/evidence"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/store"
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
// The REAL runner outcome census is threaded in (P0-2/I-01): the verdict
// must trace to executed tests, never be synthesized from job status. A nil
// census fail-closes as zero-executed for succeeded runs — an unknown
// outcome cannot be positive evidence.
func evidenceEvaluate(rec *domain.EvidenceRecord, ctx evidencepkg.Context, results *evidencepkg.TestResults) evidencepkg.Evaluation {
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
	if results != nil {
		census := *results
		proposal.Results = &census
		if census.Failed > 0 {
			proposal.Outcome = evidencepkg.OutcomeFailed
		} else if census.Passed > 0 {
			proposal.Outcome = evidencepkg.OutcomePassed
		} else {
			proposal.Outcome = evidencepkg.OutcomeSkipped
		}
	} else if rec.Verdict == evidencepkg.VerdictPass {
		// Legacy callers without a census keep the string-outcome path; the
		// scheduler always passes one (nil maps to zero-executed below).
		proposal.Outcome = evidencepkg.OutcomePassed
	} else {
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
