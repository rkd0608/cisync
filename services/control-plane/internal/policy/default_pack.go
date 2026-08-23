package policy

import (
	"encoding/json"
	"errors"
	"sync"
)

// defaultPolicyBodyJSON is the DOMAIN_MODEL_DRAFT §8 policy body embedded
// verbatim as the v1 compiled-in default pack (ARCHITECTURE.md D7).
const defaultPolicyBodyJSON = `{
  "policy_id": "pol_payments_high_risk",
  "version": 4,
  "scope_selectors": {
    "repos": ["acme/payments"],
    "paths": ["services/checkout/**", "libs/idempotency/**"],
    "risk_classes": ["high", "critical"],
    "actors": {"agents": ["agent:*"], "exclude": ["agent:docs-writer"]}
  },
  "required_evidence_by_risk": {
    "low":    ["hermetic_build", "selected_unit"],
    "medium": ["hermetic_build", "selected_unit", "api_compat"],
    "high":   ["hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"],
    "critical": ["hermetic_build", "api_compat", "full_integration", "human_approval"]
  },
  "ladder_overrides": {
    "tier3_risk_classes": ["high", "critical"],
    "min_selection_confidence": 0.98,
    "fallback_triggers": ["uncertainty_gt_0.02", "sparse_history_lt_20", "protected_paths"],
    "protected_paths": ["auth/**", "migrations/**", "infrastructure/prod/**"]
  },
  "budgets": {
    "per_candidate": {"cpu_minutes": 120, "environment_minutes": 30, "repair_attempts": 2},
    "per_tenant_hour": {"cpu_minutes": 5000, "concurrent_candidates": 40},
    "wip_by_tier": {"0": 200, "1": 60, "2": 20, "3": 6, "4": 2},
    "env_templates": {"payments-preview": {"max_concurrent": 4}},
    "value_tiers": {"acme/payments": 1.5, "acme/docs": 0.3}
  },
  "autonomy": {
    "level": 3,
    "levels_semantics": {
      "0": "observe and explain only",
      "1": "recommend tests/prioritization/cancellations; human acts",
      "2": "trigger pre-approved validation automatically",
      "3": "bounded repair attempts on isolated branches",
      "4": "mark low-risk candidates merge-eligible",
      "5": "auto-merge protected low-risk changes",
      "6": "progressive deploy under strict runtime invariants"
    },
    "auto_merge_risk_classes": [],
    "auto_repair_classes": ["compile_regression", "merge_conflict", "functional_regression"],
    "escalate_on": ["security_policy_violation", "test_expectation_drift", "classification_confidence_lt_0.8"]
  }
}`

// catchAllPolicyBodyJSON is the compiled-in wildcard fallback pack. The §8
// payments pack above only matches its scope selectors, but the REST surface
// accepts every risk class and repo; without a resolvable active policy the
// planner would fail closed on anything else (I-09). Lower specificity
// (all-wildcard selectors) means the payments pack wins whenever it matches.
const catchAllPolicyBodyJSON = `{
  "policy_id": "pol_sauron_default",
  "version": 1,
  "scope_selectors": {},
  "required_evidence_by_risk": {
    "low":      ["hermetic_build", "selected_unit"],
    "medium":   ["hermetic_build", "selected_unit", "api_compat"],
    "high":     ["hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"],
    "critical": ["hermetic_build", "api_compat", "full_integration", "human_approval"]
  },
  "ladder_overrides": {
    "tier3_risk_classes": ["high", "critical"],
    "min_selection_confidence": 0.98,
    "fallback_triggers": ["uncertainty_gt_0.02", "sparse_history_lt_20", "protected_paths"],
    "protected_paths": ["auth/**", "migrations/**", "infrastructure/prod/**"]
  },
  "budgets": {
    "per_candidate": {"cpu_minutes": 120, "environment_minutes": 30, "repair_attempts": 2},
    "per_tenant_hour": {"cpu_minutes": 5000, "concurrent_candidates": 40},
    "wip_by_tier": {"0": 200, "1": 60, "2": 20, "3": 6, "4": 2},
    "env_templates": {"payments-preview": {"max_concurrent": 4}},
    "value_tiers": {}
  },
  "autonomy": {
    "level": 3,
    "levels_semantics": {
      "0": "observe and explain only",
      "1": "recommend tests/prioritization/cancellations; human acts",
      "2": "trigger pre-approved validation automatically",
      "3": "bounded repair attempts on isolated branches",
      "4": "mark low-risk candidates merge-eligible",
      "5": "auto-merge protected low-risk changes",
      "6": "progressive deploy under strict runtime invariants"
    },
    "auto_merge_risk_classes": [],
    "auto_repair_classes": ["compile_regression", "merge_conflict", "functional_regression"],
    "escalate_on": ["security_policy_violation", "test_expectation_drift", "classification_confidence_lt_0.8"]
  }
}`

var (
	defaultOnce sync.Once
	defaultRec  PolicyRecord
	defaultErr  error

	catchAllOnce sync.Once
	catchAllRec  PolicyRecord
	catchAllErr  error
)

func parsePack(raw string, parseErr *error) PolicyRecord {
	var body PolicyBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		*parseErr = err
		return PolicyRecord{}
	}
	return PolicyRecord{ID: body.PolicyID, Version: body.Version, Status: StatusActive, Body: body}
}

// DefaultPolicyPack returns the compiled-in §8 payments pack as a single
// active PolicyRecord.
func DefaultPolicyPack() PolicyRecord {
	defaultOnce.Do(func() { defaultRec = parsePack(defaultPolicyBodyJSON, &defaultErr) })
	return defaultRec
}

// CatchAllPolicyPack returns the compiled-in wildcard fallback pack.
func CatchAllPolicyPack() PolicyRecord {
	catchAllOnce.Do(func() { catchAllRec = parsePack(catchAllPolicyBodyJSON, &catchAllErr) })
	return catchAllRec
}

// DefaultPolicyPackErr reports a parse failure of the embedded packs, if any.
func DefaultPolicyPackErr() error {
	_ = CatchAllPolicyPack()
	return errors.Join(defaultErr, catchAllErr)
}

// DefaultRegistry returns a Registry serving both compiled-in packs:
// the specific §8 payments document and the wildcard fallback. Production
// wiring may wrap or replace it behind the same interface.
func DefaultRegistry() Registry {
	return RegistryFunc(func() ([]PolicyRecord, error) {
		recs := []PolicyRecord{CatchAllPolicyPack()}
		if rec := DefaultPolicyPack(); rec.ID != "" {
			recs = append(recs, rec)
		}
		if err := DefaultPolicyPackErr(); err != nil {
			return nil, err
		}
		return recs, nil
	})
}
