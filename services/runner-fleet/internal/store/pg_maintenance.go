package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// RecordArtifacts implements Store.
func (s *PGStore) RecordArtifacts(ctx context.Context, runID string, artifacts []domain.Artifact, now time.Time) error {
	batch := &pgx.Batch{}
	for _, a := range artifacts {
		batch.Queue(`
			INSERT INTO fleet.artifacts (digest, kind, size_bytes, payload, produced_by_job, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (digest) DO NOTHING`,
			a.Digest, a.Name, a.SizeBytes, a.Content, runID, now)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("pg store: record artifacts for %s: %w", runID, err)
	}
	return nil
}

// RequeueStale implements Store.
func (s *PGStore) RequeueStale(ctx context.Context, threshold time.Duration, now time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE fleet.execution_jobs
		SET status=$3, fence_token=fence_token+1, claimed_by=NULL
		WHERE status=$2 AND last_heartbeat < $1
		RETURNING run_id`, now.Add(-threshold), domain.StatusRunning, domain.StatusQueued)
	if err != nil {
		return nil, fmt.Errorf("pg store: requeue stale: %w", err)
	}
	defer rows.Close()
	var requeued []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("pg store: scan stale run: %w", err)
		}
		requeued = append(requeued, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg store: iterate stale runs: %w", err)
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM fleet.workers WHERE last_heartbeat < $1`, now.Add(-threshold))
	if err != nil {
		return nil, fmt.Errorf("pg store: purge stale workers: %w", err)
	}
	return requeued, nil
}

// QueueDepth implements Store.
func (s *PGStore) QueueDepth(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pool, tier, count(*) FROM fleet.execution_jobs
		WHERE status=$1 GROUP BY pool, tier`, domain.StatusQueued)
	if err != nil {
		return nil, fmt.Errorf("pg store: queue depth: %w", err)
	}
	defer rows.Close()
	depth := make(map[string]int64)
	for rows.Next() {
		var pool string
		var tier int
		var n int64
		if err := rows.Scan(&pool, &tier, &n); err != nil {
			return nil, fmt.Errorf("pg store: scan queue depth: %w", err)
		}
		depth[fmt.Sprintf("%s/%d", pool, tier)] = n
	}
	return depth, rows.Err()
}
