package scheduler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	jobleasepkg "cisync.dev/cisync/control-plane/internal/joblease"
	policypkg "cisync.dev/cisync/control-plane/internal/policy"
	"cisync.dev/cisync/control-plane/internal/store"
)

// dispatchQueued ranks queued runs with the frozen priority formula and
// admits them under policy WIP caps and REAL budget counters (I-06/I-10/
// I-13). Remaining budget = policy ceiling − used(read from
// ctrl.budget_counters); no sentinel re-seeding. Admitted runs are pushed
// to the fleet (dispatch = enqueue; the WORKER claims via its own poll
// loop) and flipped to dispatched with the post-claim fence epoch — the
// SAME tx upsert-increments the tenant's budget counters by the reserved
// amounts, so a crash leaves either both or neither (conservation).
func (e *EngineScheduler) dispatchQueued(ctx context.Context) (int, error) {
	queued, err := e.store.QueuedRuns(ctx, e.batch*4)
	if err != nil {
		return 0, err
	}
	if len(queued) == 0 {
		return 0, nil
	}

	resolved := e.policy()
	ranked := rankBatch(queued)
	inFlight, err := e.store.InFlightByTier(ctx)
	if err != nil {
		return 0, err
	}
	caps := capsFromPolicy(resolved.Body.Budgets.WIPByTier)
	usage, err := e.store.BudgetUsageSnapshot(ctx, tenantsOf(queued))
	if err != nil {
		return 0, err
	}
	budgets := remainingBudgets(resolved.Body.Budgets.PerTenantHour, usage, tenantsOf(queued))
	res := Admit(ranked, caps, WIPSnapshot{InFlightByTier: inFlight}, budgets)

	// B7: budget-class admission denials are security-audit events (deduped
	// per run). Audited BEFORE any early-return so a fully-denied batch
	// still produces its rows.
	tenantsByID := make(map[string]string, len(queued))
	for _, qr := range queued {
		tenantsByID[qr.ID] = qr.TenantID
	}
	admissionsByID := make(map[string]Admission, len(queued))
	for _, a := range res.Admissions {
		if a.Admitted {
			admissionsByID[a.RunID] = a
		} else {
			e.auditBudgetAdmissionDenied(ctx, tenantsByID[a.RunID], a)
		}
	}
	if len(admissionsByID) == 0 {
		return 0, nil
	}

	dispatched := 0
	for i := range queued {
		a, ok := admissionsByID[queued[i].ID]
		if !ok {
			continue // already audited above
		}
		n, err := e.dispatchOne(ctx, queued[i].ID, BudgetReservation{
			CPUMinutes:           a.ReservedCPU,
			ConcurrentCandidates: a.ReservedConcurrent,
		}, resolved)
		if err != nil {
			logf("dispatch %s: %v", queued[i].ID, err)
			e.auditDispatchReserveExhausted(ctx, queued[i].ID, queued[i].TenantID, err)
			continue
		}
		dispatched += n
	}
	return dispatched, nil
}

// dispatchOne pushes one admitted run to the fleet and transitions it to
// dispatched inside its own tx; the conditional UPDATE makes double-dispatch
// impossible. Fence token stamps the fleet's post-first-claim epoch (0→1) so
// completion gating compares like-for-like (I-11). A job-lease token is
// minted for EVERY run at this boundary (B2/I-04): jti binds
// run/attempt/fence, exp stays within the 60-minute cap, and the fleet will
// reject every mutation of the job without it.
func (e *EngineScheduler) dispatchOne(ctx context.Context, runID string, reservation BudgetReservation, resolved policypkg.ResolvedPolicy) (int, error) {
	run, err := e.store.GetRunByID(ctx, runID)
	if err != nil {
		return 0, err
	}
	if run.State != domain.RunQueued {
		return 0, nil
	}
	if e.leaseSigner == nil {
		return 0, fmt.Errorf("dispatch %s: no job-lease signer configured", runID)
	}
	run.FenceToken = 1 // fleet bumps 0→1 on first worker claim
	leaseToken, err := e.mintJobLease(run)
	if err != nil {
		return 0, err
	}
	req := relayEnqueueRequest(run)
	req.LeaseToken = leaseToken
	if err := e.fleet.Enqueue(ctx, req); err != nil {
		return 0, err
	}
	err = e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		ev, err := store.DispatchRunTx(ctx, tx, e.store, run)
		if err != nil {
			return err
		}
		// I-06: reservation commits with the state flip. The RETURNING-based
		// reserve re-checks the policy ceiling INSIDE this tx, so concurrent
		// dispatchers can never overrun (denial rolls back the flip too).
		return store.ReserveBudgetsTx(ctx, tx, run.TenantID, ev.Seq,
			store.BudgetDeltas{
				store.BudgetCPUMinutes:           reservation.CPUMinutes,
				store.BudgetConcurrentCandidates: reservation.ConcurrentCandidates,
			},
			store.BudgetLimits{
				store.BudgetCPUMinutes:           resolved.Body.Budgets.PerTenantHour.CPUMinutes,
				store.BudgetConcurrentCandidates: resolved.Body.Budgets.PerTenantHour.ConcurrentCandidates,
			})
	})
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// mintJobLease signs the dispatch-time credential with the documented 60m
// TTL ceiling; iat/exp come from the monotonic-enough wall clock — a small
// skew against fleet verification is tolerated by the exp margin.
func (e *EngineScheduler) mintJobLease(run *domain.ValidationRun) (string, error) {
	now := timeNowUTC()
	attempt := run.Attempt
	repo := run.JobSpec.Repo
	tier := run.Tier
	return e.leaseSigner.Mint(jobleasepkg.Claims{
		Audience:   jobleasepkg.Audience,
		ID:         jobleasepkg.JTIBuilds(run.ID, attempt, run.FenceToken),
		RunID:      run.ID,
		Attempt:    attempt,
		FenceToken: run.FenceToken,
		Repo:       repo,
		Tier:       tier,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(jobleasepkg.LeaseTTLMax).Unix(),
	})
}
