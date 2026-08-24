package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	jobleasepkg "sauron.dev/sauron/control-plane/internal/joblease"
	policypkg "sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/store"
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

	admissionsByID := make(map[string]Admission, res.AdmittedCount)
	for _, a := range res.Admissions {
		if a.Admitted {
			admissionsByID[a.RunID] = a
		}
	}
	if len(admissionsByID) == 0 {
		return 0, nil
	}

	dispatched := 0
	for i := range queued {
		a, ok := admissionsByID[queued[i].ID]
		if !ok {
			continue // denied by admission (I-10): stays queued
		}
		n, err := e.dispatchOne(ctx, queued[i].ID, BudgetReservation{
			CPUMinutes:           a.ReservedCPU,
			ConcurrentCandidates: a.ReservedConcurrent,
		}, resolved)
		if err != nil {
			logf("dispatch %s: %v", queued[i].ID, err)
			continue
		}
		dispatched += n
	}
	return dispatched, nil
}

// BudgetReservation carries the I-06 amounts one run's dispatch tx commits.
type BudgetReservation struct {
	CPUMinutes           int64
	ConcurrentCandidates int64
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
func capsFromPolicy(wipByTier map[string]int) Caps {
	caps := Caps{WIPByTier: make(map[int]int, len(wipByTier))}
	for tierText, capValue := range wipByTier {
		tier := parseTier(tierText)
		if tier < 0 || capValue < 0 {
			continue
		}
		caps.WIPByTier[tier] = capValue
	}
	return caps
}

// parseTier converts the policy pack's string tier key; non-numeric keys
// yield -1 and are skipped by the caller.
func parseTier(tierText string) int {
	tier := 0
	for _, c := range tierText {
		if c < '0' || c > '9' {
			return -1
		}
		tier = tier*10 + int(c-'0')
	}
	return tier
}

// remainingBudgets derives each queued tenant's remaining budget from REAL
// counter usage against the policy ceilings. The usage snapshot only holds
// tenants that already own counter rows, so the ledger is keyed by the QUEUED
// tenants instead: a tenant without any row has consumed nothing (P0-3:
// counters start at zero implicitly) and must see the full ceiling — deriving
// from the snapshot alone funded nobody and starved all fresh tenants.
func remainingBudgets(perTenantHour policypkg.PerTenantHourBudget, usage map[string]map[store.BudgetKind]int64, tenants []string) BudgetLedger {
	b := BudgetLedger{
		TenantCPURemaining:        make(map[string]int64, len(tenants)),
		TenantConcurrentRemaining: make(map[string]int64, len(tenants)),
	}
	for _, tenantID := range tenants {
		used := usage[tenantID]
		b.TenantCPURemaining[tenantID] = perTenantHour.CPUMinutes - used[store.BudgetCPUMinutes]
		b.TenantConcurrentRemaining[tenantID] = perTenantHour.ConcurrentCandidates - used[store.BudgetConcurrentCandidates]
	}
	return b
}

func tenantsOf(queued []store.QueuedRun) []string {
	seen := make(map[string]struct{}, len(queued))
	out := make([]string, 0, len(queued))
	for _, qr := range queued {
		if _, ok := seen[qr.TenantID]; ok {
			continue
		}
		seen[qr.TenantID] = struct{}{}
		out = append(out, qr.TenantID)
	}
	return out
}
