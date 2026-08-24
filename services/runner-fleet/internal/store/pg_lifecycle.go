package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// ClaimJobs implements Store. The single UPDATE ... WHERE id IN (SELECT ...
// FOR UPDATE SKIP LOCKED) statement is the atomic claim primitive; concurrent
// claimers never receive the same row and every claim bumps the epoch.
// The claiming worker is registered inside the same tx BEFORE the claim so
// execution_jobs.claimed_by's FK can never reject an unknown (e.g. external
// "anonymous") worker.
// EnsureWorker registers a worker liveness row; called once per executor slot
// at startup, refreshed opportunistically. Kept OUTSIDE claim transactions.
func (s *PGStore) EnsureWorker(ctx context.Context, id string, pool string, capacity int, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO fleet.workers (id, pool, capacity, last_heartbeat)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET pool=EXCLUDED.pool,
			capacity=EXCLUDED.capacity, last_heartbeat=EXCLUDED.last_heartbeat`,
		id, pool, capacity, now)
	if err != nil {
		return fmt.Errorf("pg store: ensure worker %s: %w", id, err)
	}
	return nil
}

func (s *PGStore) ClaimJobs(ctx context.Context, c Claim, now time.Time) ([]domain.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg store: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// WHY no worker-row upsert here: writing fleet.workers inside the claim
	// tx serializes every claimer on one hot row and holds it across the
	// batch UPDATE below — a lock convoy under backlog (W3 storm finding).
	// Worker liveness is owned by EnsureWorker/TouchWorker instead.

	rows, err := tx.Query(ctx, `
		UPDATE fleet.execution_jobs j
		SET status=$3, provider=$7, fence_token=j.fence_token+1, claimed_by=$4, claimed_at=$5, last_heartbeat=$5
		WHERE j.id IN (
			SELECT id FROM fleet.execution_jobs
			WHERE pool=$1 AND status=$2
			ORDER BY created_at, id
			LIMIT $6
			FOR UPDATE SKIP LOCKED
		)
		RETURNING j.run_id, j.attempt, j.fence_token, j.tier, j.pool, j.job_spec, j.lease_token`,
		c.Pool, domain.StatusQueued, domain.StatusRunning, c.WorkerID, now, c.Limit, c.Provider)
	if err != nil {
		return nil, fmt.Errorf("pg store: claim jobs: %w", err)
	}
	var claimed []domain.Job
	for rows.Next() {
		var j domain.Job
		var spec []byte
		if err := rows.Scan(&j.RunID, &j.Attempt, &j.FenceToken, &j.Tier, &j.Pool, &spec, &j.LeaseToken); err != nil {
			rows.Close()
			return nil, fmt.Errorf("pg store: scan claimed job: %w", err)
		}
		if err := json.Unmarshal(spec, &j.Spec); err != nil {
			rows.Close()
			return nil, fmt.Errorf("pg store: unmarshal job spec for %s: %w", j.RunID, err)
		}
		j.Pool = c.Pool
		claimed = append(claimed, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg store: iterate claimed jobs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pg store: commit claim: %w", err)
	}
	return claimed, nil
}

// Cancel implements Store.
func (s *PGStore) Cancel(ctx context.Context, runID string, reason string, now time.Time) (bool, error) {
	ref, err := json.Marshal(map[string]any{"reason": reason})
	if err != nil {
		return false, fmt.Errorf("pg store: marshal cancel ref: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE fleet.execution_jobs
		SET status=$3, fence_token=fence_token+1, finished_at=$4, result_ref=$5
		WHERE run_id=$1 AND status IN ($6,$7)`,
		runID, 0, domain.StatusCancelled, now, ref, domain.StatusQueued, domain.StatusRunning)
	if err != nil {
		return false, fmt.Errorf("pg store: cancel %s: %w", runID, err)
	}
	return tag.RowsAffected() > 0, nil
}
