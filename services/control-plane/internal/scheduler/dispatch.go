package scheduler

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	policypkg "sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/store"
)

// dispatchQueued ranks queued runs with the frozen priority formula and
// admits them under policy WIP caps (I-10/I-13). Admitted runs are pushed to
// the fleet (§2: the WORKER claims via its own poll loop, so dispatch =
// enqueue) and flipped to dispatched with the post-claim fence epoch.
func (e *EngineScheduler) dispatchQueued(ctx context.Context) (int, error) {
	queued, err := e.store.QueuedRuns(ctx, e.batch*4)
	if err != nil {
		return 0, err
	}
	if len(queued) == 0 {
		return 0, nil
	}

	ranked := rankBatch(queued)
	inFlight, err := e.store.InFlightByTier(ctx)
	if err != nil {
		return 0, err
	}
	caps := capsFromPolicy(e.policy())
	budgets := tenantBudgets(queued)
	res := Admit(ranked, caps, WIPSnapshot{InFlightByTier: inFlight}, budgets)

	admittedIDs := make(map[string]struct{}, res.AdmittedCount)
	for _, a := range res.Admissions {
		if a.Admitted {
			admittedIDs[a.RunID] = struct{}{}
		}
	}
	if len(admittedIDs) == 0 {
		return 0, nil
	}

	dispatched := 0
	for i := range queued {
		if _, ok := admittedIDs[queued[i].ID]; !ok {
			continue // denied by admission (I-10): stays queued
		}
		n, err := e.dispatchOne(ctx, queued[i].ID)
		if err != nil {
			logf("dispatch %s: %v", queued[i].ID, err)
			continue
		}
		dispatched += n
	}
	return dispatched, nil
}

// rankBatch computes effective priorities (frozen formula + aging floor).
func rankBatch(queued []store.QueuedRun) []RankedRun {
	now := timeNowUTC()
	ranked := make([]RankedRun, 0, len(queued))
	for _, qr := range queued {
		ageHours := now.Sub(qr.CreatedAt).Hours()
		ranked = append(ranked, RankedRun{
			Run: Run{
				ID:                qr.ID,
				CandidateID:       qr.CandidateID,
				TenantID:          qr.TenantID,
				Pool:              qr.Pool,
				Tier:              qr.Tier,
				EstDurationMS:     qr.EstDurationMS,
				EstCostMillicents: qr.EstCostMillicents,
				CreatedSeq:        qr.CreatedSeq,
				CreatedULID:       qr.ID,
			},
			EffectivePriority: EffectivePriority(qr.Priority, ageHours),
		})
	}
	return ranked
}

// dispatchOne pushes one admitted run to the fleet and transitions it to
// dispatched inside its own tx; the conditional UPDATE makes double-dispatch
// impossible. Fence token stamps the fleet's post-first-claim epoch (0→1) so
// completion gating compares like-for-like (I-11).
func (e *EngineScheduler) dispatchOne(ctx context.Context, runID string) (int, error) {
	run, err := e.store.GetRunByID(ctx, runID)
	if err != nil {
		return 0, err
	}
	if run.State != domain.RunQueued {
		return 0, nil
	}
	if err := e.fleet.Enqueue(ctx, relayEnqueueRequest(run)); err != nil {
		return 0, err
	}
	run.FenceToken = 1 // fleet bumps 0→1 on first worker claim
	err = e.store.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := store.DispatchRunTx(ctx, tx, e.store, run)
		return err
	})
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// jobSpecToMap renders the typed spec into the wire representation.
func jobSpecToMap(spec domain.JobSpec) map[string]any {
	raw, err := json.Marshal(spec)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// capsFromPolicy converts the §8 wip_by_tier JSON map into admission Caps.
// A tier missing from the pack stays unconfigured ⇒ fail-closed denial.
func capsFromPolicy(wipByTier map[int]int) Caps {
	caps := Caps{WIPByTier: make(map[int]int, len(wipByTier))}
	for tier, capValue := range wipByTier {
		if capValue < 0 {
			continue
		}
		caps.WIPByTier[tier] = capValue
	}
	return caps
}

// tenantBudgets seeds per-tenant budget ledgers.
//
// WHY static budgets: v1 dev posture enforces the WIP dimension of
// admission; per-tenant CPU/concurrency reservation accounting lands with
// the I-06 budget ledger events. Generous sentinels keep Admit's
// conservation math intact without inventing new schema.
func tenantBudgets(queued []store.QueuedRun) BudgetLedger {
	rec := policypkg.DefaultPolicyPack()
	cpuBudget := rec.Body.Budgets.PerTenantHour.CPUMinutes
	concBudget := rec.Body.Budgets.PerTenantHour.ConcurrentCandidates
	if cpuBudget <= 0 {
		cpuBudget = 5000
	}
	if concBudget <= 0 {
		concBudget = 40
	}
	b := BudgetLedger{
		TenantCPURemaining:        map[string]int64{},
		TenantConcurrentRemaining: map[string]int64{},
	}
	for _, qr := range queued {
		if _, seen := b.TenantCPURemaining[qr.TenantID]; !seen {
			b.TenantCPURemaining[qr.TenantID] = cpuBudget
			b.TenantConcurrentRemaining[qr.TenantID] = concBudget
		}
	}
	return b
}
