package planner

// Ladder tier numbers (§3).
const (
	TierAdmission   = 0
	TierLocal       = 1
	TierContract    = 2
	TierSystem      = 3
	TierIntegration = 4
)

// Per-tier envelopes from the §3 table: timeout seconds and cost estimate in
// millicents ($0.05, $0.20, $1.50, $12, $40).
const (
	TimeoutTier0Sec = 60
	TimeoutTier1Sec = 900
	TimeoutTier2Sec = 1800
	TimeoutTier3Sec = 3600
	TimeoutTier4Sec = 5400

	CostTier0Millicents = int64(5000)
	CostTier1Millicents = int64(20000)
	CostTier2Millicents = int64(150000)
	CostTier3Millicents = int64(1200000)
	CostTier4Millicents = int64(4000000)
)

// Execution pools referenced by scheduler contention telemetry.
const (
	PoolFast     = "fast"
	PoolUnit     = "unit"
	PoolContract = "contract"
	PoolSystem   = "system"
	PoolTrain    = "merge_train"
)

type envelope struct {
	timeoutSeconds int
	costMillicents int64
	pool           string
}

var tierEnvelope = map[int]envelope{
	TierAdmission:   {TimeoutTier0Sec, CostTier0Millicents, PoolFast},
	TierLocal:       {TimeoutTier1Sec, CostTier1Millicents, PoolUnit},
	TierContract:    {TimeoutTier2Sec, CostTier2Millicents, PoolContract},
	TierSystem:      {TimeoutTier3Sec, CostTier3Millicents, PoolSystem},
	TierIntegration: {TimeoutTier4Sec, CostTier4Millicents, PoolTrain},
}

// JobSpec is one planned job. Field names match domain.JobSpec where they
// overlap; adapters map 1:1.
type JobSpec struct {
	Name              string
	Kind              string
	TimeoutSeconds    int
	EstCostMillicents int64
	Pool              string
}

func job(tier int, name, kind string) JobSpec {
	env := tierEnvelope[tier]
	return JobSpec{Name: name, Kind: kind, TimeoutSeconds: env.timeoutSeconds, EstCostMillicents: env.costMillicents, Pool: env.pool}
}

// Default and full-suite job catalogs per tier (§3 "Default jobs" column;
// fallback triggers widen the *selected* jobs of tiers 1–2 to their
// full-suite variants). hermetic_build appears in tier 1 because every risk
// class requires it.
var (
	tier0Jobs = []JobSpec{
		job(TierAdmission, "secret_scan", "secret_scan"),
		job(TierAdmission, "format_lint", "format_lint"),
		job(TierAdmission, "typecheck_lite", "typecheck_lite"),
		job(TierAdmission, "diff_sanity", "diff_sanity"),
		job(TierAdmission, "policy_admissibility", "policy_admissibility"),
	}

	tier1SelectedJobs = []JobSpec{
		job(TierLocal, "hermetic_build", "hermetic_build"),
		job(TierLocal, "compile_affected_targets", "compile"),
		job(TierLocal, "selected_unit_tests", "selected_unit"),
		job(TierLocal, "sast_diff_scan", "sast_diff"),
	}
	tier1FullJobs = []JobSpec{
		job(TierLocal, "hermetic_build", "hermetic_build"),
		job(TierLocal, "compile_affected_targets", "compile"),
		job(TierLocal, "full_unit_suite", "selected_unit"),
		job(TierLocal, "sast_diff_scan", "sast_diff"),
	}

	tier2SelectedBase = []JobSpec{
		job(TierContract, "impacted_integration_tests", "integration"),
		job(TierContract, "api_compat", "api_compat"),
		job(TierContract, "schema_migration_compat", "migration_compat"),
		job(TierContract, "dependency_license_check", "license_check"),
	}
	tier2FullBase = []JobSpec{
		job(TierContract, "full_integration_suite", "integration"),
		job(TierContract, "api_compat", "api_compat"),
		job(TierContract, "schema_migration_compat", "migration_compat"),
		job(TierContract, "dependency_license_check", "license_check"),
	}
	tier2PaymentJob     = job(TierContract, "payment_contract_check", "payment_contract")
	tier2IdempotencyJob = job(TierContract, "idempotency_race_probe", "idempotency_race")

	tier3Jobs = []JobSpec{
		job(TierSystem, "e2e_browser_mobile", "e2e"),
		job(TierSystem, "load_affected_surfaces", "load"),
		job(TierSystem, "fuzz_affected_surfaces", "fuzz"),
		job(TierSystem, "preview_env_validation", "preview_env"),
	}

	tier4Jobs = []JobSpec{
		job(TierIntegration, "merge_train_simulation", "integration_train"),
		job(TierIntegration, "integrated_build_sign", "integrated_build"),
		job(TierIntegration, "canary_gate", "canary_gate"),
	}
)

// TierDefaultCost returns the §3 cost constant for a tier; unknown tiers map
// to the tier-2 default.
func TierDefaultCost(tier int) int64 {
	if env, ok := tierEnvelope[tier]; ok {
		return env.costMillicents
	}
	return CostTier2Millicents
}

// TierDefaultTimeoutSeconds returns the §3 timeout for a tier; unknown tiers
// map to the tier-2 default.
func TierDefaultTimeoutSeconds(tier int) int {
	if env, ok := tierEnvelope[tier]; ok {
		return env.timeoutSeconds
	}
	return TimeoutTier2Sec
}
