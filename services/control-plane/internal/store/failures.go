package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InsertFailureCaseTx persists a failure_cases projection row and is invoked
// inside the completion effect tx (invariant I-12: dedupe + effects atomic).
func InsertFailureCaseTx(ctx context.Context, tx pgx.Tx, tenantID, fcID string, seq int64,
	candidateID, runID, signature, classification string, confidence float64,
	reproductionCommand string, suspectedPaths []string, routedAction, state string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO ctrl.failure_cases (tenant_id, id, seq, candidate_id, run_id,
		   signature_digest, classification, classification_confidence,
		   reproduction_command, suspected_paths, routed_action, state, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())`,
		tenantID, fcID, seq, candidateID, runID, signature, classification, confidence,
		reproductionCommand, toTextSlice(suspectedPaths), routedAction, state)
	if err != nil {
		return fmt.Errorf("insert failure case: %w", err)
	}
	return nil
}

// OpenFailureCaseForRun returns an open failure case id for a run, if any.
func (s *Store) OpenFailureCaseForRun(ctx context.Context, tenantID, runID string) (string, bool, error) {
	var fcID string
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM ctrl.failure_cases
		 WHERE tenant_id=$1 AND run_id=$2 AND state IN ('open','classified','routed')
		 ORDER BY created_at DESC LIMIT 1`, tenantID, runID).Scan(&fcID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open failure case: %w", err)
	}
	return fcID, true, nil
}
