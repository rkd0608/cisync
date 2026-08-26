package scheduler

import (
	"encoding/json"

	"sauron.dev/sauron/control-plane/internal/domain"
	policypkg "sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/store"
)

// dispatch_ranking.go holds the pure ranking/admission-support functions,
// split from dispatch.go for the charter's 250-line file cap: they map
// queued runs onto RankedRuns and translate policy budgets into Caps.

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
