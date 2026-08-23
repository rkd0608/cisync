package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

// PGStore is the Postgres-backed Store over schema fleet (exclusive write
// ownership per ARCHITECTURE §2).
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore connects to the DSN and pings before returning.
func NewPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg store: parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg store: ping: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

// Close releases the pool.
func (s *PGStore) Close() {
	s.pool.Close()
}

// Enqueue implements Store.
func (s *PGStore) Enqueue(ctx context.Context, job domain.Job) error {
	spec, err := json.Marshal(job.Spec)
	if err != nil {
		return fmt.Errorf("pg store: marshal job spec: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO fleet.execution_jobs (run_id, attempt, pool, tier, status, fence_token, job_spec)
		VALUES ($1,$2,$3,$4,$5,0,$6)
		ON CONFLICT (run_id) DO NOTHING`,
		job.RunID, job.Attempt, job.Pool, job.Tier, domain.StatusQueued, spec)
	if err != nil {
		return fmt.Errorf("pg store: enqueue %s: %w", job.RunID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pg store: enqueue %s: duplicate run_id", job.RunID)
	}
	return nil
}

// ClaimJobs implements Store. The single UPDATE ... WHERE id IN (SELECT ...
// FOR UPDATE SKIP LOCKED) statement is the atomic claim primitive; concurrent
// claimers never receive the same row and every claim bumps the epoch.
func (s *PGStore) ClaimJobs(ctx context.Context, c Claim, now time.Time) ([]domain.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg store: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
		RETURNING j.run_id, j.attempt, j.fence_token, j.tier, j.pool, j.job_spec`,
		c.Pool, domain.StatusQueued, domain.StatusRunning, c.WorkerID, now, c.Limit, c.Provider)
	if err != nil {
		return nil, fmt.Errorf("pg store: claim jobs: %w", err)
	}
	var claimed []domain.Job
	for rows.Next() {
		var j domain.Job
		var spec []byte
		if err := rows.Scan(&j.RunID, &j.Attempt, &j.FenceToken, &j.Tier, &j.Pool, &spec); err != nil {
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
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO fleet.workers (id, pool, capacity, last_heartbeat)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET pool=EXCLUDED.pool, last_heartbeat=EXCLUDED.last_heartbeat`,
		c.WorkerID, c.Pool, c.Limit, now); err != nil {
		return nil, fmt.Errorf("pg store: upsert worker: %w", err)
	}
	return claimed, nil
}

// Get implements Store.
func (s *PGStore) Get(ctx context.Context, runID string) (FleetJob, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, run_id, attempt, tier, pool, status, fence_token, COALESCE(claimed_by,''),
		       COALESCE(claimed_at, 'epoch'::timestamptz), COALESCE(last_heartbeat, 'epoch'::timestamptz),
		       COALESCE(finished_at, 'epoch'::timestamptz), COALESCE(result_ref, '{}'::jsonb), accepted, job_spec, created_at
		FROM fleet.execution_jobs WHERE run_id=$1`, runID)
	var j FleetJob
	var spec []byte
	var resultRef []byte
	err := row.Scan(&j.ID, &j.RunID, &j.Attempt, &j.Tier, &j.Pool, &j.Status, &j.FenceToken, &j.ClaimedBy,
		&j.ClaimedAt, &j.LastHeartbeat, &j.FinishedAt, &resultRef, &j.Accepted, &spec, &j.CreatedAt)
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
	resultRef := map[string]any{
		"status":          c.Status,
		"logs_digest":     c.LogsDigest,
		"duration_ms":     c.DurationMS,
		"cost_millicents": c.ActualCostMilliCents,
	}
	if c.Classification != "" {
		resultRef["class"] = c.Classification
	}
	if len(c.ArtifactDigests) > 0 {
		resultRef["artifact_digests"] = c.ArtifactDigests
	}
	ref, err := json.Marshal(resultRef)
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
