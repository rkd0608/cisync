package policy

import (
	"encoding/json"
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

var (
	defaultOnce sync.Once
	defaultRec  PolicyRecord
	defaultErr  error
)

// DefaultPolicyPack returns the compiled-in §8 default pack as a single
// active PolicyRecord. Parsing failures (impossible for the embedded text)
// surface via the record's zero Body.
func DefaultPolicyPack() PolicyRecord {
	defaultOnce.Do(func() {
		var body PolicyBody
		if err := json.Unmarshal([]byte(defaultPolicyBodyJSON), &body); err != nil {
			defaultErr = err
			return
		}
		defaultRec = PolicyRecord{ID: body.PolicyID, Version: body.Version, Status: StatusActive, Body: body}
	})
	return defaultRec
}

// DefaultPolicyPackErr reports a parse failure of the embedded pack, if any.
func DefaultPolicyPackErr() error { return defaultErr }

// DefaultRegistry returns a Registry serving exactly the compiled-in default
// pack. Production wiring may wrap or replace it behind the same interface.
func DefaultRegistry() Registry {
	return RegistryFunc(func() ([]PolicyRecord, error) {
		rec := DefaultPolicyPack()
		if rec.ID == "" {
			return nil, DefaultPolicyPackErr()
		}
		return []PolicyRecord{rec}, nil
	})
}
