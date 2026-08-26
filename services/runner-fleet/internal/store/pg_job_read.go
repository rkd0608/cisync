package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// Get implements Store.
func (s *PGStore) Get(ctx context.Context, runID string) (FleetJob, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, run_id, attempt, tier, pool, status, fence_token, COALESCE(claimed_by,''),
		       COALESCE(claimed_at, 'epoch'::timestamptz), COALESCE(last_heartbeat, 'epoch'::timestamptz),
		       COALESCE(finished_at, 'epoch'::timestamptz), COALESCE(result_ref, '{}'::jsonb), accepted, job_spec,
		       COALESCE(lease_token,''), created_at
		FROM fleet.execution_jobs WHERE run_id=$1`, runID)
	var j FleetJob
	var spec []byte
	var resultRef []byte
	err := row.Scan(&j.ID, &j.RunID, &j.Attempt, &j.Tier, &j.Pool, &j.Status, &j.FenceToken, &j.ClaimedBy,
		&j.ClaimedAt, &j.LastHeartbeat, &j.FinishedAt, &resultRef, &j.Accepted, &spec, &j.LeaseToken, &j.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return FleetJob{}, fmt.Errorf("pg store: get %s: %w", runID, domain.ErrNotFound)
		}
		return FleetJob{}, fmt.Errorf("pg store: get %s: %w", runID, err)
	}
	if err := json.Unmarshal(spec, &j.Spec); err != nil {
		return FleetJob{}, fmt.Errorf("pg store: unmarshal spec: %w", err)
	}
	if err := json.Unmarshal(resultRef, &j.ResultRef); err != nil {
		j.ResultRef = map[string]any{}
	}
	return j, nil
}

// Heartbeat implements Store.
func (s *PGStore) Heartbeat(ctx context.Context, runID string, fenceToken int64, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE fleet.execution_jobs
		SET last_heartbeat=$3
		WHERE run_id=$1 AND status=$4 AND fence_token=$2`,
		runID, fenceToken, now, domain.StatusRunning)
	if err != nil {
		return fmt.Errorf("pg store: heartbeat %s: %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		j, err := s.Get(ctx, runID)
		if err != nil {
			return fmt.Errorf("pg store: heartbeat lookup: %w", err)
		}
		if j.Status != domain.StatusRunning || j.FenceToken != fenceToken {
			return fmt.Errorf("pg store: heartbeat %s: %w", runID, domain.ErrFenceMismatch)
		}
		return fmt.Errorf("pg store: heartbeat %s: transient state", runID)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE fleet.workers SET last_heartbeat=$2
		WHERE id = (SELECT claimed_by FROM fleet.execution_jobs WHERE run_id=$1)`,
		runID, now)
	if err != nil {
		return fmt.Errorf("pg store: heartbeat worker refresh: %w", err)
	}
	return nil
}

// Complete implements Store. Fence check + acceptance happen in ONE statement
// so only the current epoch can flip a job terminal, exactly once (I-11).
func (s *PGStore) Complete(ctx context.Context, runID string, c Completion, now time.Time) error {
	ref, err := json.Marshal(c.ResultRef())
	if err != nil {
		return fmt.Errorf("pg store: marshal result_ref: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE fleet.execution_jobs
		SET status=$3, accepted=true, finished_at=$4, result_ref=$5
		WHERE run_id=$1 AND status=$6 AND fence_token=$2 AND accepted=false`,
		runID, c.FenceToken, c.Status, now, ref, domain.StatusRunning)
	if err != nil {
		return fmt.Errorf("pg store: complete %s: %w", runID, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	j, err := s.Get(ctx, runID)
	if err != nil {
		return fmt.Errorf("pg store: complete lookup: %w", err)
	}
	if j.Accepted {
		return fmt.Errorf("pg store: complete %s: %w", runID, domain.ErrAlreadyAccepted)
	}
	return fmt.Errorf("pg store: complete %s: %w", runID, domain.ErrFenceMismatch)
}

// TerminalAccepted implements Store.
func (s *PGStore) TerminalAccepted(ctx context.Context, limit int) ([]FleetJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, attempt, tier, pool, status, fence_token,
		       COALESCE(claimed_by,''), COALESCE(claimed_at,'epoch'::timestamptz),
		       COALESCE(last_heartbeat,'epoch'::timestamptz), COALESCE(finished_at,'epoch'::timestamptz),
		       COALESCE(result_ref,'{}'::jsonb), accepted, job_spec, COALESCE(lease_token,''), created_at
		FROM fleet.execution_jobs
		WHERE accepted AND status IN ($1,$2,$3,$4)
		ORDER BY finished_at DESC LIMIT $5`,
		domain.StatusSucceeded, domain.StatusFailed, domain.StatusTimedOut, domain.StatusCancelled, limit)
	if err != nil {
		return nil, fmt.Errorf("pg store: terminal accepted: %w", err)
	}
	defer rows.Close()
	var out []FleetJob
	for rows.Next() {
		var j FleetJob
		var spec []byte
		var resultRef []byte
		if err := rows.Scan(&j.ID, &j.RunID, &j.Attempt, &j.Tier, &j.Pool, &j.Status, &j.FenceToken,
			&j.ClaimedBy, &j.ClaimedAt, &j.LastHeartbeat, &j.FinishedAt,
			&resultRef, &j.Accepted, &spec, &j.LeaseToken, &j.CreatedAt); err != nil {
			return nil, fmt.Errorf("pg store: scan terminal job: %w", err)
		}
		if err := json.Unmarshal(spec, &j.Spec); err != nil {
			return nil, fmt.Errorf("pg store: unmarshal spec for %s: %w", j.RunID, err)
		}
		if err := json.Unmarshal(resultRef, &j.ResultRef); err != nil {
			j.ResultRef = map[string]any{}
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
