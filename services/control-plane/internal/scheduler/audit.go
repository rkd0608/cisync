package scheduler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/audit"
	"sauron.dev/sauron/control-plane/internal/domain"
	evidencepkg "sauron.dev/sauron/control-plane/internal/evidence"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// SetAuditObserver wires the metric callback fired after every successful
// security-audit emission (sauron_security_audit_total{kind}). Optional:
// unset keeps emissions metric-free in unit tests.
func (e *EngineScheduler) SetAuditObserver(notify func(kind string)) {
	e.auditMu.Lock()
	e.auditNotify = notify
	e.auditMu.Unlock()
}

func (e *EngineScheduler) notifyAudit(kind audit.Kind) {
	e.auditMu.Lock()
	notify := e.auditNotify
	e.auditMu.Unlock()
	if notify != nil {
		notify(string(kind))
	}
}

// emitAudit persists one audit row outside any open transaction (pool-level)
// and reports the emission. WHY best-effort: audit sinks run inside the
// completion/admission loops; a failed audit INSERT must never fail the
// triggering pipeline — it is logged and counted instead.
func (e *EngineScheduler) emitAudit(ctx context.Context, ev audit.Event, evErr error) {
	if evErr != nil {
		logf("audit event build failed (%s): %v", ev.Kind, evErr)
		return
	}
	if err := e.store.InsertSecurityAudit(ctx, ev); err != nil {
		logf("security audit %s persist failed: %v", ev.Kind, err)
		return
	}
	e.notifyAudit(ev.Kind)
}

// absorbStaleCompletion emits the B7 fence_mismatch audit row and marks the
// stale feed key processed inside ONE tx, so the audit row exists exactly
// when the diagnostic absorption does (I-12).
func (e *EngineScheduler) absorbStaleCompletion(ctx context.Context, run *domain.ValidationRun, job relay.CompletedJob) error {
	ev, err := audit.New(run.TenantID, audit.KindFenceMismatch,
		audit.Actor{Kind: string(domain.ActorSystem), ID: "scheduler"},
		map[string]any{"run_id": run.ID, "candidate_id": run.CandidateID},
		map[string]any{
			"attempt":         job.Attempt,
			"presented_fence": job.FenceToken,
			"expected_fence":  run.FenceToken,
			"run_state":       string(run.State),
			"rejection":       "stale_fence_token",
		})
	if err != nil {
		return err
	}
	key := dedupeKey(job.RunID, job.FenceToken)
	err = e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.MarkProcessedTx(ctx, tx, completionConsumer, key); err != nil {
			return err
		}
		if err := e.store.InsertSecurityAuditTx(ctx, tx, ev); err != nil {
			return err
		}
		return store.ClearFeedRetriesTx(ctx, tx, completionConsumer, []string{key})
	})
	if err != nil {
		logf("stale-completion absorption %s@%d failed: %v", job.RunID, job.FenceToken, err)
		return err
	}
	e.notifyAudit(ev.Kind)
	return nil
}

// auditFenceMismatch records the I-11 stale-fence rejection of one fleet
// completion (B7: fence-token mismatches rejected at fleet complete).
// Retained for callers outside the absorption path; the completion consumer
// uses absorbStaleCompletion for atomic exactly-once semantics.
func (e *EngineScheduler) auditFenceMismatch(ctx context.Context, run *domain.ValidationRun, presentedFence int64, attempt int) {
	ev, err := audit.New(run.TenantID, audit.KindFenceMismatch,
		audit.Actor{Kind: string(domain.ActorSystem), ID: "scheduler"},
		map[string]any{"run_id": run.ID, "candidate_id": run.CandidateID},
		map[string]any{
			"attempt":         attempt,
			"presented_fence": presentedFence,
			"expected_fence":  run.FenceToken,
			"run_state":       string(run.State),
			"rejection":       "stale_fence_token",
		})
	e.emitAudit(ctx, ev, err)
}

// auditEvidenceTamperTx records validator rulings that indicate tampering:
// quarantine decisions (digest-manifest mismatch) and provenance rejections
// (lease-jti binding failures). Ordinary I-01/I-02 rejections stay out of
// the security stream — they are expected validation traffic, not attacks.
func (e *EngineScheduler) auditEvidenceTamperTx(ctx context.Context, tx pgx.Tx, run *domain.ValidationRun, kind string, outcome evidencepkg.Evaluation) {
	tamper := outcome.Action == evidencepkg.ActionQuarantine ||
		outcome.Reason == evidencepkg.ReasonProvenanceMismatch
	if !tamper {
		return
	}
	ev, err := audit.New(run.TenantID, audit.KindEvidenceTamper,
		audit.Actor{Kind: string(domain.ActorSystem), ID: "scheduler"},
		map[string]any{"run_id": run.ID, "candidate_id": run.CandidateID, "kind": kind},
		map[string]any{
			"action": string(outcome.Action),
			"reason": outcome.Reason,
		})
	if err != nil {
		logf("audit event build failed: %v", err)
		return
	}
	if err := e.store.InsertSecurityAuditTx(ctx, tx, ev); err != nil {
		// Same-tx failure would abort the effect pipeline for an audit row;
		// the ruling itself is already ledger-visible, so shed the row.
		logf("security audit %s persist failed: %v", ev.Kind, err)
		return
	}
	e.notifyAudit(ev.Kind)
}

// auditBudgetAdmissionDenied emits one B7 budget_exceeded row per queued run
// whose admission was denied by a BUDGET-class reason (I-10), deduped per
// run so one stuck run cannot flood the stream at tick cadence. WIP-cap
// denials are capacity, not budget violations, so they never audit.
func (e *EngineScheduler) auditBudgetAdmissionDenied(ctx context.Context, tenantID string, adm Admission) {
	switch adm.DenyReason {
	case DenyTenantCPU, DenyTenantConc:
	default:
		return
	}
	e.deniedMu.Lock()
	_, dup := e.deniedAudits[adm.RunID]
	if !dup {
		e.deniedAudits[adm.RunID] = struct{}{}
	}
	e.deniedMu.Unlock()
	if dup {
		return // exactly-once per run, not per tick
	}
	ev, err := audit.New(tenantID, audit.KindBudgetExceeded,
		audit.Actor{Kind: string(domain.ActorSystem), ID: "scheduler"},
		map[string]any{"run_id": adm.RunID, "candidate_id": adm.CandidateID},
		map[string]any{"deny_reason": adm.DenyReason, "scope": "admission_denied"})
	e.emitAudit(ctx, ev, err)
}

// auditDispatchReserveExhausted records the hard I-06 reservation failure
// that rolled back a dispatch (concurrent overrun past the advisory
// snapshot). Emitted AFTER the rollback — same-tx is impossible because the
// tx aborted.
func (e *EngineScheduler) auditDispatchReserveExhausted(ctx context.Context, runID, tenantID string, cause error) {
	if cause == nil || !errorsIsBudget(cause) {
		return // storage errors are not budget violations
	}
	ev, err := audit.New(tenantID, audit.KindBudgetExceeded,
		audit.Actor{Kind: string(domain.ActorSystem), ID: "scheduler"},
		map[string]any{"run_id": runID},
		map[string]any{"reason": cause.Error(), "scope": "dispatch_reserve"})
	if err != nil {
		logf("audit event build failed: %v", err)
		return
	}
	if err := e.store.InsertSecurityAudit(ctx, ev); err != nil {
		logf("security audit %s persist failed: %v", ev.Kind, err)
		return
	}
	e.notifyAudit(ev.Kind)
}

func errorsIsBudget(err error) bool {
	return errors.Is(err, store.ErrBudgetExhausted)
}
