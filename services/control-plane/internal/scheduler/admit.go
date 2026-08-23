package scheduler

// Denial reasons (I-10: overflow ⇒ deny, never overrun).
const (
	DenyWIPCap     = "wip_cap_exceeded"
	DenyTenantCPU  = "tenant_cpu_budget_exhausted"
	DenyTenantConc = "tenant_concurrency_cap_reached"
)

// Caps holds configured WIP caps per tier. A tier without a configured cap
// denies admission (fail-closed).
type Caps struct {
	WIPByTier map[int]int
}

// WIPSnapshot counts in-flight runs per tier at admission time.
type WIPSnapshot struct {
	InFlightByTier map[int]int
}

// BudgetLedger is the remaining budget state admission draws against.
type BudgetLedger struct {
	// TenantCPURemaining maps tenant → remaining cpu-minutes of the
	// per-tenant-hour budget.
	TenantCPURemaining map[string]int64
	// TenantConcurrentRemaining maps tenant → remaining concurrent-candidate
	// slots.
	TenantConcurrentRemaining map[string]int64
}

// Admission is the per-run ruling, emitted in I-13 processing order.
type Admission struct {
	RunID       string
	CandidateID string
	Admitted    bool
	DenyReason  string
}

// Deltas are exactly the reservations taken by admitted runs. Conservation
// (I-06): applying Deltas to the input state equals the post-admission
// state; no denied run contributes any delta.
type Deltas struct {
	CPUMinutesByTenant map[string]int64
	ConcurrentByTenant map[string]int64
	WIPAddedByTier     map[int]int
}

// BatchResult is the deterministic outcome of one admission pass over a
// batch.
type BatchResult struct {
	Admissions    []Admission // sorted by effective priority (I-13)
	Deltas        Deltas
	AdmittedCount int
}

// Admit performs atomic reservation semantics as a pure function: each run
// is admitted only if its WIP slot, cpu-minute reservation and concurrency
// slot ALL fit; a run that fails any check reserves nothing. Runs are
// processed in I-13 priority order regardless of input permutation, so the
// result is deterministic for identical inputs.
//
// CPU need = ceil(est_duration_ms / 60000) minutes; concurrency consumes one
// distinct candidate slot per tenant per batch (runs of the same candidate
// share the slot within the batch).
func Admit(batch []RankedRun, caps Caps, wip WIPSnapshot, budgets BudgetLedger) BatchResult {
	ordered := append([]RankedRun(nil), batch...)
	SortRanked(ordered)

	wipNow := copyIntMap(wip.InFlightByTier)
	cpu := copyI64Map(budgets.TenantCPURemaining)
	conc := copyI64Map(budgets.TenantConcurrentRemaining)

	res := BatchResult{Admissions: make([]Admission, 0, len(ordered)), Deltas: Deltas{}}
	seenCandidates := make(map[string]map[string]struct{}) // tenant → candidate set

	for _, rr := range ordered {
		run := rr.Run
		adm := Admission{RunID: run.ID, CandidateID: run.CandidateID}

		capTier, capped := caps.WIPByTier[run.Tier]
		inFlight := wipNow[run.Tier]
		switch {
		case !capped:
			adm.DenyReason = DenyWIPCap
		case inFlight+1 > capTier:
			adm.DenyReason = DenyWIPCap
		default:
			need := cpuMinutes(run.EstDurationMS)
			if cpu[run.TenantID] < need {
				adm.DenyReason = DenyTenantCPU
				break
			}
			concNeed := int64(0)
			if seenCandidates[run.TenantID] == nil {
				seenCandidates[run.TenantID] = make(map[string]struct{})
			}
			if _, dup := seenCandidates[run.TenantID][run.CandidateID]; !dup {
				concNeed = 1
			}
			if conc[run.TenantID] < concNeed {
				adm.DenyReason = DenyTenantConc
				break
			}
			// Atomic reservation: apply every dimension together.
			wipNow[run.Tier]++
			cpu[run.TenantID] -= need
			conc[run.TenantID] -= concNeed
			seenCandidates[run.TenantID][run.CandidateID] = struct{}{}
			adm.Admitted = true
			recordDelta(&res.Deltas, run.TenantID, run.Tier, need, concNeed)
			res.AdmittedCount++
		}
		res.Admissions = append(res.Admissions, adm)
	}
	return res
}

func cpuMinutes(estMS int64) int64 {
	if estMS <= 0 {
		return 0
	}
	return (estMS + 59999) / 60000
}

func recordDelta(d *Deltas, tenant string, tier int, cpuNeed, concNeed int64) {
	if d.CPUMinutesByTenant == nil {
		d.CPUMinutesByTenant = make(map[string]int64)
	}
	if d.ConcurrentByTenant == nil {
		d.ConcurrentByTenant = make(map[string]int64)
	}
	if d.WIPAddedByTier == nil {
		d.WIPAddedByTier = make(map[int]int)
	}
	if cpuNeed > 0 {
		d.CPUMinutesByTenant[tenant] += cpuNeed
	}
	if concNeed > 0 {
		d.ConcurrentByTenant[tenant] += concNeed
	}
	d.WIPAddedByTier[tier]++
}

func copyIntMap(m map[int]int) map[int]int {
	out := make(map[int]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyI64Map(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
