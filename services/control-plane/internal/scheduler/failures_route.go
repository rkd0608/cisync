package scheduler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	failurepkg "sauron.dev/sauron/control-plane/internal/failure"
	policypkg "sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// onRunFailed classifies the failure, records the FailureCase, and routes:
// infra_transient retries bounded; repairable classes authorize a bounded
// repair; security violations reject; everything else defers (EC-016/021).
func (e *EngineScheduler) onRunFailed(ctx context.Context, tx pgx.Tx, run *domain.ValidationRun, job relay.CompletedJob, seq int64) error {
	fc := failurepkg.Classify(job.LogsExcerpt, failurepkg.Context{})
	intent, err := e.store.GetIntent(ctx, run.TenantID, intentIDForCandidate(ctx, e.store, run.TenantID, run.CandidateID))
	if err != nil {
		return fmt.Errorf("load intent for routing: %w", err)
	}

	fcID := domain.NewID(domain.PrefixFailure)
	classifiedEvent, err := newFailureClassifiedEvent(run.TenantID, fcID, run, fc)
	if err != nil {
		return err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{classifiedEvent}); err != nil {
		return err
	}
	routedAction := routeFailure(fc)
	if err := store.InsertFailureCaseTx(ctx, tx, run.TenantID, fcID, classifiedEvent.Seq,
		run.CandidateID, run.ID, fc.SignatureDigest, fc.Classification,
		fc.ClassificationConfidence, fc.ReproductionCommand, fc.SuspectedPaths,
		routedAction, "classified"); err != nil {
		return err
	}

	switch {
	case fc.Classification == failurepkg.ClassInfraTransient && run.Attempt < e.maxRetry:
		return e.retryRun(ctx, tx, run)
	case routedAction == "reject":
		return e.rejectCandidate(ctx, tx, seq, run, "security or policy violation never auto-waived")
	case routedAction == "quarantine_flake":
		return e.deferCandidate(ctx, tx, deferReq(run), "flake quarantined; human triage pending")
	case routedAction == "repair":
		return e.authorizeRepairOrDefer(ctx, tx, run, fc, fcID, intent, classifiedEvent.Seq)
	default:
		return e.deferCandidate(ctx, tx, deferReq(run), "failure class awaiting escalation")
	}
}

// intentContextForCandidate loads the owning intent of a candidate.
func intentIDForCandidate(ctx context.Context, st *store.Store, tenantID, candidateID string) string {
	cand, err := st.GetCandidate(ctx, tenantID, candidateID)
	if err != nil {
		return ""
	}
	return cand.IntentID
}

// retryRun re-queues the failed run with attempt++ and a fresh fence token
// (bounded infra-transient retry per §1.5).
func (e *EngineScheduler) retryRun(ctx context.Context, tx pgx.Tx, run *domain.ValidationRun) error {
	if err := run.Apply("run.retry"); err != nil {
		return fmt.Errorf("retry transition: %w", err)
	}
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	ev, err := domain.NewEvent(run.TenantID,
		domain.AggregateRef{Type: string(domain.AggRun), ID: run.ID},
		"validation.requested", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"run_id":                  run.ID,
			"plan_id":                 run.PlanID,
			"candidate_id":            run.CandidateID,
			"tier":                    run.Tier,
			"est_duration_ms":         run.EstDurationMS,
			"est_cost_millicents":     run.EstCostMillicents,
			"priority":                run.Priority,
			"cancellation_conditions": map[string]any{},
			"pool":                    run.Pool,
		})
	if err != nil {
		return err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE ctrl.validation_runs SET state='queued', attempt=$3, fence_token=$4, seq=$5, finished_at=NULL
		 WHERE id=$1 AND tenant_id=$2`,
		run.ID, run.TenantID, run.Attempt, run.FenceToken, ev.Seq)
	return err
}

// authorizeRepairOrDefer runs the repair authorization gate: authorized
// repairs park the candidate in repairing; denials defer the decision.
func (e *EngineScheduler) authorizeRepairOrDefer(ctx context.Context, tx pgx.Tx,
	run *domain.ValidationRun, fc failurepkg.FailureCase, fcID string, intent *domain.Intent, seq int64) error {

	resolved, err := policypkg.Resolve(policypkg.Subject{
		Repo:         intent.Declared.Repo,
		ChangedPaths: intent.Declared.OwnedSurfaces,
		RiskClass:    string(intent.Declared.RiskClass),
	}, policypkg.DefaultRegistry())
	policyRef := domain.PolicyRef{}
	var prohibited []string
	if err == nil {
		policyRef = domain.PolicyRef{PolicyID: resolved.PolicyID, Version: resolved.Version}
		prohibited = resolved.Body.LadderOverrides.ProtectedPaths
	} else {
		resolved = engineResolvedPolicy()
		prohibited = resolved.Body.LadderOverrides.ProtectedPaths
	}
	auth := failurepkg.AuthorizeRepair(fc, resolved, failurepkg.RepairContext{
		IntentOwnedSurfaces: intent.Declared.OwnedSurfaces,
		ProhibitedPaths:     prohibited,
		RiskClass:           string(intent.Declared.RiskClass),
	})
	if !auth.Authorized {
		return e.deferCandidate(ctx, tx, deferReq(run),
			fmt.Sprintf("repair denied: %s", auth.DenyReason))
	}
	_ = policyRef // stamped on the eventual decision via the active pack
	repairID := domain.NewID(domain.PrefixRepair)
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	ev, err := domain.NewEvent(run.TenantID,
		domain.AggregateRef{Type: string(domain.AggRepairTask), ID: repairID},
		"repair.authorized", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"repair_id": repairID,
			"envelope": map[string]any{
				"reproduction_command": auth.Envelope.ReproductionCommand,
				"allowed_paths":        toAnySlice(auth.Envelope.AllowedPaths),
				"prohibited_paths":     toAnySlice(auth.Envelope.ProhibitedPaths),
				"max_iterations":       auth.Envelope.MaxIterations,
			},
		})
	if err != nil {
		return err
	}
	if err := e.store.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return err
	}
	envelopeJSON, err := domain.CanonicalJSON(map[string]any{
		"reproduction_command": auth.Envelope.ReproductionCommand,
		"failed_assertion":     auth.Envelope.FailedAssertion,
		"allowed_paths":        toAnySlice(auth.Envelope.AllowedPaths),
		"prohibited_paths":     toAnySlice(auth.Envelope.ProhibitedPaths),
		"max_iterations":       auth.Envelope.MaxIterations,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.repair_tasks (tenant_id, id, seq, failure_case_id, candidate_id, envelope, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'authorized',now())`,
		run.TenantID, repairID, ev.Seq, fcID, run.CandidateID, envelopeJSON); err != nil {
		return fmt.Errorf("insert repair task: %w", err)
	}
	return store.MarkCandidateStateTx(ctx, tx, run.TenantID, run.CandidateID,
		string(domain.CandRepairing), []string{"submitted", "planned", "validating"}, ev.Seq)
}

// engineResolvedPolicy adapts the compiled-in default pack for the repair
// gate when registry resolution fails (dev posture keeps gates working).
func engineResolvedPolicy() policypkg.ResolvedPolicy {
	rec := policypkg.DefaultPolicyPack()
	return policypkg.ResolvedPolicy{PolicyID: rec.ID, Version: rec.Version, Body: rec.Body}
}

func (e *EngineScheduler) rejectCandidate(ctx context.Context, tx pgx.Tx, _ int64, run *domain.ValidationRun, reason string) error {
	return e.renderDecision(ctx, tx, renderRequest{
		TenantID:    run.TenantID,
		CandidateID: run.CandidateID,
		Verb:        domain.VerbRejected,
		Policy:      domain.DefaultPolicy().Ref,
		Summary:     reason,
	})
}

func (e *EngineScheduler) deferCandidate(ctx context.Context, tx pgx.Tx, req renderRequest, reason string) error {
	req.Verb = domain.VerbDeferred
	req.Summary = reason
	if req.Policy.PolicyID == "" {
		req.Policy = domain.DefaultPolicy().Ref
	}
	return e.renderDecision(ctx, tx, req)
}

func deferReq(run *domain.ValidationRun) renderRequest {
	return renderRequest{TenantID: run.TenantID, CandidateID: run.CandidateID}
}

func newFailureClassifiedEvent(tenantID, fcID string, run *domain.ValidationRun, fc failurepkg.FailureCase) (*domain.Event, error) {
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	return domain.NewEvent(tenantID,
		domain.AggregateRef{Type: string(domain.AggFailureCase), ID: fcID},
		"failure.classified", "", domain.NewCorrelationID(), actor,
		map[string]any{
			"fc_id":                fcID,
			"run_id":               run.ID,
			"classification":       fc.Classification,
			"confidence":           fc.ClassificationConfidence,
			"signature_digest":     fc.SignatureDigest,
			"suspected_paths":      toAnySlice(fc.SuspectedPaths),
			"reproduction_command": fc.ReproductionCommand,
		})
}

func routeForClass(classification string) string {
	switch classification {
	case failurepkg.ClassInfraTransient:
		return "retry"
	case failurepkg.ClassKnownFlake, failurepkg.ClassProbableFlake:
		return "quarantine_flake"
	case failurepkg.ClassCompileRegression, failurepkg.ClassFunctionalRegression, failurepkg.ClassMergeConflict:
		return "repair"
	case failurepkg.ClassSecurityPolicyViolation:
		return "reject"
	default:
		return "escalate_human"
	}
}

// routeFailure gates the class table with the autonomy confidence floor: the
// unclassified fallback (no rule matched, confidence 0.30) escalates to human
// triage instead of auto-authorizing a repair nobody can apply in v1 — the
// classifier contract explicitly routes sub-floor classifications to
// escalation (autonomy.escalate_on classification_confidence_lt_0.8).
func routeFailure(fc failurepkg.FailureCase) string {
	if fc.RuleID == "unclassified_fallback" {
		return "escalate_human"
	}
	return routeForClass(fc.Classification)
}
