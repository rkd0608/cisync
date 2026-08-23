package store

import (
	"context"
	"fmt"
)

// Failed-required-run accounting for the decision-freshness gate: an
// eligible_for_merge_train verdict is forbidden while any plan-required run
// of the candidate sits in an unresolved failure (failed/timed_out). The
// failure router owns retry/repair/defer/reject for those runs; this count
// only blocks premature eligible rendering.

// CountFailedRequiredRuns counts the candidate's runs whose job kind is in
// requiredKinds and whose state is a permanent failure. Runs requeued by the
// bounded infra-transient retry leave failed state, so a successful retry
// unblocks eligibility (the run's terminal state then reflects the outcome
// the decision must reflect).
func (s *Store) CountFailedRequiredRuns(ctx context.Context, tenantID, candidateID string, requiredKinds []string) (int, error) {
	if len(requiredKinds) == 0 {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT count(*) FROM ctrl.validation_runs
		 WHERE tenant_id=$1 AND candidate_id=$2
		   AND state IN ('failed','timed_out')
		   AND job_spec->>'kind' = ANY($3)`,
		tenantID, candidateID, requiredKinds)
	if err != nil {
		return 0, fmt.Errorf("count failed required runs: %w", err)
	}
	defer rows.Close()
	var n int
	if !rows.Next() {
		return 0, rows.Err()
	}
	if err := rows.Scan(&n); err != nil {
		return 0, fmt.Errorf("scan failed required runs: %w", err)
	}
	return n, nil
}
