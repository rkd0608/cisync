// Package scheduler ranks validation runs by the frozen priority formula,
// admits them under WIP caps and budget reservations as a pure function
// (I-06/I-10), orders with deterministic priority→age→ULID tie-breaking
// (I-13), and propagates supersede/cancel decisions to run level without
// wrong-job kills.
package scheduler

// Formula constants (DOMAIN_MODEL_DRAFT §4).
const (
	// AgingFloorSlopePerHour is the aging slope: floor(age) =
	// age_hours * 0.01.
	AgingFloorSlopePerHour = 0.01
	// AgingFloorCap bounds the aging floor at 0.5.
	AgingFloorCap = 0.5

	// DeadlineWindowHours is the deadline-proximity normalization window.
	DeadlineWindowHours = 72.0
	// PrerequisiteBoost multiplies urgency when the candidate is a
	// prerequisite of others.
	PrerequisiteBoost = 1.25
	// StalenessWeightPerHour weights base-SHA staleness into urgency.
	StalenessWeightPerHour = 0.005

	// DefaultDecisionChangeRate is the sparse-data default when learned
	// stats have no observation for the key (max uncertainty ⇒ run it).
	DefaultDecisionChangeRate = 0.5
	// DefaultBusinessValue applies when policy has no value_tiers entry.
	DefaultBusinessValue = 1.0
)

// Risk reduction static map by risk_class (§4); critical shares high's
// reduction (the §4 table lists low|medium|high only).
const (
	RiskReductionHigh     = 1.0
	RiskReductionMedium   = 0.7
	RiskReductionLow      = 0.4
	RiskReductionCritical = RiskReductionHigh
	BlastRadiusDivisor    = 20.0
)

// Run is the scheduler's view of one queued validation run. CreatedSeq is
// the ledger sequence at enqueue time — the logical clock all ordering
// derives from (I-13); wall clocks are advisory.
type Run struct {
	ID                string
	CandidateID       string
	TenantID          string
	Pool              string
	Tier              int
	JobKind           string
	EstDurationMS     int64
	EstCostMillicents int64 // 0 ⇒ planner tier default applies
	CreatedSeq        int64
	CreatedULID       string
}

// Telemetry carries everything the formula needs beyond the run itself.
type Telemetry struct {
	DecisionRateKnown    bool    // false ⇒ DefaultDecisionChangeRate
	DecisionChangeRate   float64 // P(decision_changes | run) from stats.test_outcomes
	RiskClass            string
	DownstreamDependents int // blast radius from dep graph
	HasDeadline          bool
	HoursToDeadline      float64
	IsPrerequisite       bool    // prerequisite-of others
	BaseStalenessHours   float64 // base_sha age vs repo HEAD
	BusinessValue        float64 // ≤0 ⇒ DefaultBusinessValue
	QueueDepth           int     // queue_depth{pool,tier}
	PoolCapacity         int     // ≤0 ⇒ contention neutral (1.0)
}

// Priority computes the frozen formula:
//
//	priority = P(decision_changes) * risk_reduction * urgency * business_value
//	           / (cost_est * contention_penalty)
//
// Guards keep the function total: unknown decision rate → 0.5, missing
// business value → 1.0, zero cost → planner tier default, non-positive pool
// capacity → contention 1.0.
func Priority(r Run, t Telemetry) float64 {
	numerator := decisionChangeRate(t) * riskReduction(t) * urgency(t) * businessValue(t)
	denominator := float64(costEst(r)) * contentionPenalty(t)
	if denominator <= 0 {
		denominator = 1
	}
	return numerator / denominator
}

// AgingFloor returns min(age_hours * slope, cap): starvation protection that
// eventually lifts every queued run (EC-025).
func AgingFloor(ageHours float64) float64 {
	if ageHours < 0 {
		ageHours = 0
	}
	f := ageHours * AgingFloorSlopePerHour
	if f > AgingFloorCap {
		return AgingFloorCap
	}
	return f
}

// EffectivePriority is max(base_priority, aging_floor(age_hours)).
func EffectivePriority(basePriority, ageHours float64) float64 {
	f := AgingFloor(ageHours)
	if basePriority > f {
		return basePriority
	}
	return f
}

func decisionChangeRate(t Telemetry) float64 {
	if !t.DecisionRateKnown {
		return DefaultDecisionChangeRate
	}
	r := t.DecisionChangeRate
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

func riskReduction(t Telemetry) float64 {
	var base float64
	switch t.RiskClass {
	case "critical":
		base = RiskReductionCritical
	case "high":
		base = RiskReductionHigh
	case "medium":
		base = RiskReductionMedium
	default:
		base = RiskReductionLow
	}
	return base * blastRadiusFactor(t.DownstreamDependents)
}

// blastRadiusFactor = min(1, dependents/20); non-positive counts mean the
// dep graph has no data, which is treated as neutral rather than zero so
// leaf-package work is never starved by a missing projection.
func blastRadiusFactor(dependents int) float64 {
	if dependents <= 0 {
		return 1
	}
	f := float64(dependents) / BlastRadiusDivisor
	if f > 1 {
		return 1
	}
	return f
}

func urgency(t Telemetry) float64 {
	u := 0.5
	if t.HasDeadline {
		proximity := 1 - t.HoursToDeadline/DeadlineWindowHours
		if proximity < 0 {
			proximity = 0
		}
		if proximity > 1 {
			proximity = 1
		}
		u = 0.5 + 0.5*proximity
	}
	if t.IsPrerequisite {
		u *= PrerequisiteBoost
	}
	stale := t.BaseStalenessHours
	if stale < 0 {
		stale = 0
	}
	return u + stale*StalenessWeightPerHour
}

func businessValue(t Telemetry) float64 {
	if t.BusinessValue <= 0 {
		return DefaultBusinessValue
	}
	return t.BusinessValue
}

func costEst(r Run) int64 {
	if r.EstCostMillicents > 0 {
		return r.EstCostMillicents
	}
	return tierDefaultCostMillicents(r.Tier)
}

func tierDefaultCostMillicents(tier int) int64 {
	switch tier {
	case 0:
		return 5000
	case 1:
		return 20000
	case 2:
		return 150000
	case 3:
		return 1200000
	case 4:
		return 4000000
	default:
		return 150000
	}
}

func contentionPenalty(t Telemetry) float64 {
	if t.PoolCapacity <= 0 {
		return 1.0
	}
	return float64(t.QueueDepth+1) / float64(t.PoolCapacity+1)
}
