package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	evidencepkg "sauron.dev/sauron/control-plane/internal/evidence"
	plannerpkg "sauron.dev/sauron/control-plane/internal/planner"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// onRunSucceeded proposes one evidence record per required kind the tier
// covers, runs the provenance evaluation (I-01/I-02/I-03), then renders the
// merge-train decision when plan sufficiency hits 1 (D8).
func (e *EngineScheduler) onRunSucceeded(ctx context.Context, tx pgx.Tx, run *domain.ValidationRun, job relay.CompletedJob, seq int64) error {
	plan, err := e.store.ActivePlanForCandidate(ctx, run.TenantID, run.CandidateID)
	if err != nil {
		return fmt.Errorf("load plan: %w", err)
	}
	// One run == one producing job == at most one required kind (I-03 keeps
	// one accepted record per (run_id, attempt)). Gate-only runs carry kinds
	// outside the required vocabulary and contribute nothing.
	kind := run.JobSpec.Kind
	required := plan.RequiredEvidenceKinds
	isRequired := false
	for _, k := range required {
		if k == kind {
			isRequired = true
			break
		}
	}
	if !isRequired {
		return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan)
	}
	leaseJTI := fmt.Sprintf("fleet:%s:%d", run.ID, job.FenceToken)
	prior, err := e.store.AcceptedEvidenceForRun(ctx, run.TenantID, run.ID)
	if err != nil {
		return err
	}

	rec := domain.NewEvidenceRecord(domain.NewID(domain.PrefixEvidence),
		run.TenantID, run.ID, run.Attempt, run.CandidateID, kind,
		evidencepkg.VerdictPass, job.ArtifactDigests, plan.InputsHash,
		jobSelectionConfidence(plan), job.CostMillicents, leaseJTI, time.Now().UTC())
	outcome := evidenceEvaluate(rec, evidenceContext(plan, leaseJTI, prior))
	if outcome.Action != evidencepkg.ActionAccept {
		logf("evidence %s for run %s rejected: %s", kind, run.ID, outcome.Reason)
		return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan)
	}
	ev, err := newEvidenceRecordedEvent(rec)
	if err != nil {
		return err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return err
	}
	rec.Status = domain.EvidenceAccepted
	if err := store.InsertEvidenceTx(ctx, tx, rec, ev.Seq); err != nil {
		return err
	}
	return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan)
}

// maybeRenderEligible renders eligible_for_merge_train once every required
// kind has an accepted record (sufficiency == 1 per D8).
func (e *EngineScheduler) maybeRenderEligible(ctx context.Context, tx pgx.Tx, tenantID, candidateID string, plan *domain.ValidationPlan) error {
	// Decisions are immutable facts; only the FIRST time sufficiency hits 1
	// renders one (replays of the completion feed must not duplicate).
	existing, err := e.store.LatestDecisionForCandidate(ctx, tenantID, candidateID)
	if err == nil && existing != nil {
		return nil
	}
	if err != nil && err != domain.ErrNotFound {
		return err
	}
	accepted, err := e.store.AcceptedEvidenceRefsForCandidate(ctx, tenantID, candidateID)
	if err != nil {
		return err
	}
	sufficiency := evidenceSufficiency(plan.RequiredEvidenceKinds, accepted)
	if sufficiency < 1 {
		return nil
	}
	refs := make([]string, 0, len(accepted))
	for _, ref := range accepted {
		refs = append(refs, ref.ID)
	}
	return e.renderDecision(ctx, tx, renderRequest{
		TenantID:     tenantID,
		CandidateID:  candidateID,
		PlanID:       plan.ID,
		Verb:         domain.VerbEligibleForMergeTrain,
		Policy:       plan.Policy,
		Summary:      "all required evidence kinds accepted",
		EvidenceRefs: refs,
		InputsHash:   plan.InputsHash,
	})
}

func jobSelectionConfidence(plan *domain.ValidationPlan) float64 {
	var conf float64 = 0.9
	for _, t := range plan.Tiers {
		if t.SelectionConfidence != nil && *t.SelectionConfidence > conf {
			conf = *t.SelectionConfidence
		}
	}
	return conf
}

// newEvidenceRecordedEvent builds the evidence.recorded CORE event.
func newEvidenceRecordedEvent(rec *domain.EvidenceRecord) (*domain.Event, error) {
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	return domain.NewEvent(rec.TenantID,
		domain.AggregateRef{Type: string(domain.AggEvidence), ID: rec.ID},
		"evidence.recorded", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"ev_id":           rec.ID,
			"run_id":          rec.RunID,
			"candidate_id":    rec.CandidateID,
			"kind":            rec.Kind,
			"verdict":         rec.Verdict,
			"digests":         toAnySlice(rec.Digests),
			"inputs_hash":     rec.InputsHash,
			"confidence":      rec.Confidence,
			"cost_millicents": rec.CostMillicents,
		})
}

var _ = plannerpkg.RequiredKindsCoveredByTier
