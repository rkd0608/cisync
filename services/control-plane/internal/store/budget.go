package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// BudgetKind enumerates the I-06 counter dimensions.
type BudgetKind string

// The four frozen budget kinds (INVARIANTS I-06).
const (
	BudgetCPUMinutes           BudgetKind = "cpu_minutes"
	BudgetEnvironmentMinutes   BudgetKind = "environment_minutes"
	BudgetRepairAttempts       BudgetKind = "repair_attempts"
	BudgetConcurrentCandidates BudgetKind = "concurrent_candidates"
)

// ErrBudgetExhausted denies a reservation that would exceed the policy
// limit; the run stays queued (I-10: overflow is refused, never overrun).
var ErrBudgetExhausted = fmt.Errorf("budget limit exceeded")

// BudgetDeltas are per-kind reservation/release amounts for one tenant.
type BudgetDeltas map[BudgetKind]int64

// BudgetLimits are the policy-enforced ceilings per kind.
type BudgetLimits map[BudgetKind]int64

// ReserveBudgetsTx atomically upsert-increments the counters inside the
// caller's transaction (the SAME tx that flips validation_runs
// queued→dispatched) and enforces the policy limits on the RETURNING used
// value: any kind crossing its ceiling fails the whole reservation, rolling
// back with the state flip. One UPDATE per kind — atomic, no read-modify-write.
func ReserveBudgetsTx(ctx context.Context, tx pgx.Tx, tenantID string, seq int64, deltas BudgetDeltas, limits BudgetLimits) error {
	for kind, delta := range deltas {
		if delta <= 0 {
			continue
		}
		limit := limits[kind]
		if limit <= 0 {
			return fmt.Errorf("%w: %s for %s (no configured limit)", ErrBudgetExhausted, kind, tenantID)
		}
		var used int64
		err := tx.QueryRow(ctx,
			`INSERT INTO ctrl.budget_counters (tenant_id, kind, used, updated_seq)
			 VALUES ($1,$2,$3,$4)
			 ON CONFLICT (tenant_id, kind)
			 DO UPDATE SET used = ctrl.budget_counters.used + EXCLUDED.used,
			               updated_seq = EXCLUDED.updated_seq
			 RETURNING used`,
			tenantID, string(kind), delta, seq,
		).Scan(&used)
		if err != nil {
			return fmt.Errorf("reserve %s for %s: %w", kind, tenantID, err)
		}
		if used > limit {
			return fmt.Errorf("%w: %s used=%d limit=%d for %s", ErrBudgetExhausted, kind, used, limit, tenantID)
		}
	}
	return nil
}

// ReleaseBudgetsTx decrements counters by actual amounts (fallback est) in
// the SAME transaction that drives the terminal state transition. GREATEST
// guards against transient negative usage from crash-window replays.
func ReleaseBudgetsTx(ctx context.Context, tx pgx.Tx, tenantID string, seq int64, deltas BudgetDeltas) error {
	for kind, delta := range deltas {
		if delta <= 0 {
			continue
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ctrl.budget_counters SET used = GREATEST(0, used - $3), updated_seq=$4
			 WHERE tenant_id=$1 AND kind=$2`,
			tenantID, string(kind), delta, seq)
		if err != nil {
			return fmt.Errorf("release %s for %s: %w", kind, tenantID, err)
		}
		if tag.RowsAffected() == 0 {
			// Counter row absent ⇒ nothing was ever reserved; releasing zero
			// keeps Σreservations − Σreleases == used trivially true.
			continue
		}
	}
	return nil
}

// BudgetUsageSnapshot reads current per-tenant usage so the tick's admission
// pass computes remaining budgets WITHOUT re-seeding sentinels (P0-3).
func (s *Store) BudgetUsageSnapshot(ctx context.Context, tenants []string) (map[string]map[BudgetKind]int64, error) {
	out := make(map[string]map[BudgetKind]int64, len(tenants))
	if len(tenants) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT tenant_id, kind, used FROM ctrl.budget_counters WHERE tenant_id = ANY($1)`,
		tenants)
	if err != nil {
		return nil, fmt.Errorf("budget snapshot: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tenantID string
		var kind string
		var used int64
		if err := rows.Scan(&tenantID, &kind, &used); err != nil {
			return nil, fmt.Errorf("scan budget row: %w", err)
		}
		if out[tenantID] == nil {
			out[tenantID] = make(map[BudgetKind]int64, 4)
		}
		out[tenantID][BudgetKind(kind)] = used
	}
	return out, rows.Err()
}

// ActualCPUMinutes derives released cpu minutes from actual duration,
// falling back to the estimate when the job reported no measurable time.
func ActualCPUMinutes(actualDurationMS, estDurationMS int64) int64 {
	minutes := (actualDurationMS + 59999) / 60000
	if minutes <= 0 {
		minutes = (estDurationMS + 59999) / 60000
	}
	return minutes
}
