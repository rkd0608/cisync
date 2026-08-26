package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"cisync.dev/cisync/runner-fleet/internal/domain"
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
		INSERT INTO fleet.execution_jobs (id, run_id, attempt, pool, tier, status, fence_token, job_spec, lease_token)
		VALUES ($7,$1,$2,$3,$4,$5,0,$6,$8)
		ON CONFLICT (run_id) DO NOTHING`,
		job.RunID, job.Attempt, job.Pool, job.Tier, domain.StatusQueued, spec, "job_"+job.RunID, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("pg store: enqueue %s: %w", job.RunID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pg store: enqueue %s: %w", job.RunID, domain.ErrDuplicateRun)
	}
	return nil
}
