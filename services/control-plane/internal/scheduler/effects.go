package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	evidencepkg "sauron.dev/sauron/control-plane/internal/evidence"
	jobleasepkg "sauron.dev/sauron/control-plane/internal/joblease"
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
		if err == domain.ErrNotFound {
			// P1-4: a completed run whose plan vanished can never apply;
			// typed so the feed row is absorbed instead of poisoning ticks.
			return fmt.Errorf("load plan for %s: %w", run.CandidateID,
				permanentCompletion(domain.ErrNotFound))
		}
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
		return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan, nil)
	}
	leaseJTI := jobleasepkg.JTIBuilds(run.ID, run.Attempt, job.FenceToken)
	prior, err := e.store.AcceptedEvidenceForRun(ctx, run.TenantID, run.ID)
	if err != nil {
		return err
	}

	census := evidenceCensusFromJob(job)
	rec := domain.NewEvidenceRecord(domain.NewID(domain.PrefixEvidence),
		run.TenantID, run.ID, run.Attempt, run.CandidateID, kind,
		evidencepkg.VerdictPass, job.ArtifactDigests, plan.InputsHash,
		jobSelectionConfidence(plan), job.CostMillicents, leaseJTI, time.Now().UTC())
	outcome := evidenceEvaluate(rec, evidenceContext(plan, leaseJTI, prior), census)
	if outcome.Action != evidencepkg.ActionAccept {
		logf("evidence %s for run %s rejected: %s", kind, run.ID, outcome.Reason)
		// B7: tamper-grade rulings (quarantine / provenance mismatch) are
		// security-audit events committed in THIS tx; ordinary rejections
		// are filtered inside the helper.
		e.auditEvidenceTamperTx(ctx, tx, run, kind, outcome)
		return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan, nil)
	}
	ev, err := newEvidenceRecordedEvent(rec, outcome.Meta)
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
	// The just-inserted row is INVISIBLE to pool reads until commit, so the
	// sufficiency check must count it explicitly (see maybeRenderEligible).
	return e.maybeRenderEligible(ctx, tx, run.TenantID, run.CandidateID, plan,
		&store.EvidenceRef{ID: rec.ID, Kind: kind})
}

// maybeRenderEligible renders eligible_for_merge_train once every required
// kind has an accepted record (sufficiency == 1 per D8) AND no unresolved
// required-kind failure / pending repair blocks the verb (decision must
// reflect ALL completed evidence).
//
// extra carries the CURRENT transaction's own accepted record: the just-
// inserted evidence row is invisible to other connections until commit, so
// sufficiency would always compute <1 without it.
//
// P1-5 (W4 audit): ALL four advisory reads run on the caller's tx via the
// pgxQuerier seam — never on s.Pool while this tx holds
// pg_advisory_xact_lock (cross-connection starvation stalled decisions).
func (e *EngineScheduler) maybeRenderEligible(ctx context.Context, tx pgx.Tx, tenantID, candidateID string, plan *domain.ValidationPlan, extra *store.EvidenceRef) error {
	// Decisions are immutable facts; only the FIRST time sufficiency hits 1
	// renders one (replays of the completion feed must not duplicate).
	existing, err := store.LatestDecisionForCandidateTx(ctx, tx, tenantID, candidateID)
	if err == nil && existing != nil {
		return nil
	}
	if err != nil && err != domain.ErrNotFound {
		return err
	}
	failedRequired, err := store.CountFailedRequiredRunsTx(ctx, tx, tenantID, candidateID, plan.RequiredEvidenceKinds)
	if err != nil {
		return fmt.Errorf("count failed required runs: %w", err)
	}
	candState, err := store.CandidateStateByIDTx(ctx, tx, tenantID, candidateID)
	if err != nil && err != domain.ErrNotFound {
		return fmt.Errorf("load candidate state: %w", err)
	}
	if reason := eligibilityBlockReason(candState, failedRequired); reason != "" {
		logf("eligible blocked for %s: %s", candidateID, reason)
		return nil
	}
	accepted, err := store.AcceptedEvidenceRefsForCandidateTx(ctx, tx, tenantID, candidateID)
	if err != nil {
		return err
	}
	if extra != nil {
		accepted = append(accepted, *extra)
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

// evidenceCensusFromJob maps the feed's outcome census onto the validator
// type. A completion WITHOUT a census fail-closes to zero-executed (P0-2):
// an unknown outcome can never be positive pass evidence.
func evidenceCensusFromJob(job relay.CompletedJob) *evidencepkg.TestResults {
	if job.Results == nil {
		return &evidencepkg.TestResults{}
	}
	return &evidencepkg.TestResults{
		Total:       job.Results.Total,
		Passed:      job.Results.Passed,
		Failed:      job.Results.Failed,
		Skipped:     job.Results.Skipped,
		Quarantined: job.Results.Quarantined,
	}
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
// outcomeMeta carries accept-time annotations (e.g. skipped_as_non_evidence)
// so dossiers can show exactly which outcomes were excluded by I-01.
func newEvidenceRecordedEvent(rec *domain.EvidenceRecord, outcomeMeta map[string]string) (*domain.Event, error) {
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	payload := map[string]any{
		"ev_id":           rec.ID,
		"run_id":          rec.RunID,
		"candidate_id":    rec.CandidateID,
		"kind":            rec.Kind,
		"verdict":         rec.Verdict,
		"digests":         toAnySlice(rec.Digests),
		"inputs_hash":     rec.InputsHash,
		"confidence":      rec.Confidence,
		"cost_millicents": rec.CostMillicents,
	}
	if len(outcomeMeta) > 0 {
		payload["outcome_meta"] = outcomeMeta
	}
	return domain.NewEvent(rec.TenantID,
		domain.AggregateRef{Type: string(domain.AggEvidence), ID: rec.ID},
		"evidence.recorded", "", domain.NewCorrelationID(), actor,
		payload)
}

var _ = plannerpkg.RequiredKindsCoveredByTier
