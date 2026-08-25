package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres-backed Store over schema ghconn (exclusive write
// ownership per ARCHITECTURE §2).
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore connects to the DSN and pings before returning.
func NewPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("ghconn store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ghconn store: ping: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

// Migrate applies any not-yet-applied *.up.sql files under dir in name order,
// tracking versions in ghconn.schema_migrations.
func (s *PGStore) Migrate(ctx context.Context, dir string) error {
	if _, err := s.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS ghconn`); err != nil {
		return fmt.Errorf("ghconn store: ensure schema: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS ghconn.schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
	); err != nil {
		return fmt.Errorf("ghconn store: ensure migrations table: %w", err)
	}
	entries, err := migrationFiles(dir)
	if err != nil {
		return err
	}
	for _, m := range entries {
		var applied bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM ghconn.schema_migrations WHERE version=$1)`, m.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("ghconn store: migration lookup %d: %w", m.version, err)
		}
		if applied {
			continue
		}
		if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("ghconn store: apply %s: %w", m.file, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO ghconn.schema_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the pool.
func (s *PGStore) Close() { s.pool.Close() }

// GetCheckReport implements Store.
func (s *PGStore) GetCheckReport(ctx context.Context, decisionID string) (*CheckReport, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT decision_id, candidate_id, repo, head_sha, verb, conclusion, check_run_id, dry_run, created_at
		 FROM ghconn.check_reports WHERE decision_id=$1`, decisionID)
	var rep CheckReport
	var verb string
	err := row.Scan(&rep.DecisionID, &rep.CandidateID, &rep.Repo, &rep.HeadSHA, &verb,
		&rep.Conclusion, &rep.CheckRunID, &rep.DryRun, &rep.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("ghconn store: get check report: %w", err)
	}
	rep.Verb = domainVerb(verb)
	return &rep, nil
}

// SaveCheckReport implements Store. Kept as the legacy single-insert path;
// lifecycle-aware callers use RecordCheckReport (update-in-place semantics).
func (s *PGStore) SaveCheckReport(ctx context.Context, rep CheckReport) error {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO ghconn.check_reports (decision_id, candidate_id, repo, head_sha, verb, conclusion, check_run_id, dry_run)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (decision_id) DO NOTHING`,
		rep.DecisionID, rep.CandidateID, rep.Repo, rep.HeadSHA, string(rep.Verb),
		rep.Conclusion, rep.CheckRunID, rep.DryRun)
	if err != nil {
		return fmt.Errorf("ghconn store: save check report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDuplicate
	}
	return nil
}
