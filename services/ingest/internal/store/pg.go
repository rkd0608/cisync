package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"sauron.dev/sauron/ingest/internal/domain"
)

// pgErrCodeUnique is the SQLSTATE for unique_violation.
const pgErrCodeUnique = "23505"

// PGStore is the Postgres-backed Store over schema ingest (exclusive write
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

// Ping reports database liveness.
func (s *PGStore) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("pg store: ping: %w", err)
	}
	return nil
}

// InsertDelivery implements Store. Dedup authority is the partial unique
// index over (source, ext_delivery_id) WHERE sig_ok: valid deliveries collide
// (duplicate GUID), quarantined sig_ok=false audit rows never do. Any unique
// violation maps to ErrDuplicateDelivery (D1).
func (s *PGStore) InsertDelivery(ctx context.Context, d domain.Delivery) error {
	headers, err := json.Marshal(d.Headers)
	if err != nil {
		return fmt.Errorf("pg store: marshal headers: %w", err)
	}
	var payload json.RawMessage = d.Payload
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO ingest.deliveries
			(id, source, ext_delivery_id, event_kind, repo, sig_ok, headers, payload, status, attempts, received_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT DO NOTHING`,
		d.ID, d.Source, d.ExtDeliveryID, d.EventKind, d.Repo, d.SigOK, headers, payload,
		d.Status, d.Attempts, d.ReceivedAt)
	if err != nil {
		if pgErr := newPgUniqueViolation(err); pgErr != nil {
			return fmt.Errorf("pg store: insert delivery: %w", domain.ErrDuplicateDelivery)
		}
		return fmt.Errorf("pg store: insert delivery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pg store: insert delivery: %w", domain.ErrDuplicateDelivery)
	}
	return nil
}

// GetDelivery implements Store.
func (s *PGStore) GetDelivery(ctx context.Context, source, extDeliveryID string) (domain.Delivery, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, source, ext_delivery_id, event_kind, repo, sig_ok, headers, payload, status, attempts, received_at, last_attempt_at, forwarded_at
		FROM ingest.deliveries WHERE source=$1 AND ext_delivery_id=$2`, source, extDeliveryID)
	var d domain.Delivery
	var headers []byte
	var payload []byte
	var lastAttemptAt, forwardedAt *time.Time
	err := row.Scan(&d.ID, &d.Source, &d.ExtDeliveryID, &d.EventKind, &d.Repo, &d.SigOK,
		&headers, &payload, &d.Status, &d.Attempts, &d.ReceivedAt, &lastAttemptAt, &forwardedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Delivery{}, fmt.Errorf("pg store: get delivery: %w", domain.ErrNotFound)
		}
		return domain.Delivery{}, fmt.Errorf("pg store: get delivery: %w", err)
	}
	if err := json.Unmarshal(headers, &d.Headers); err != nil {
		return domain.Delivery{}, fmt.Errorf("pg store: unmarshal headers: %w", err)
	}
	d.Payload = payload
	if lastAttemptAt != nil {
		d.LastAttemptAt = *lastAttemptAt
	}
	if forwardedAt != nil {
		d.ForwardedAt = *forwardedAt
	}
	return d, nil
}

// UpdateForwardState implements Store.
func (s *PGStore) UpdateForwardState(ctx context.Context, id string, status string, attempts int, lastAttemptAt time.Time, forwardedAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingest.deliveries
		SET status=$2, attempts=$3, last_attempt_at=$4, forwarded_at=$5
		WHERE id=$1`,
		id, status, attempts, lastAttemptAt, nullableTime(forwardedAt))
	if err != nil {
		return fmt.Errorf("pg store: update forward state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pg store: update forward state: %w", domain.ErrNotFound)
	}
	return nil
}

// DueDeliveries implements Store. Quarantined rejected rows are audit-only
// and never retried.
func (s *PGStore) DueDeliveries(ctx context.Context, olderThan time.Duration, maxAttempts int, limit int) ([]domain.Delivery, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source, ext_delivery_id, event_kind, repo, sig_ok, status, attempts, received_at, COALESCE(last_attempt_at, received_at), payload
		FROM ingest.deliveries
		WHERE status = ANY($1) AND attempts < $2
		  AND COALESCE(last_attempt_at, received_at) <= $3
		ORDER BY received_at
		LIMIT $4`,
		[]string{domain.StatusPending, domain.StatusForwardFailed}, maxAttempts, time.Now().Add(-olderThan), limit)
	if err != nil {
		return nil, fmt.Errorf("pg store: due deliveries: %w", err)
	}
	defer rows.Close()
	var out []domain.Delivery
	for rows.Next() {
		var d domain.Delivery
		var payload []byte
		if err := rows.Scan(&d.ID, &d.Source, &d.ExtDeliveryID, &d.EventKind, &d.Repo, &d.SigOK,
			&d.Status, &d.Attempts, &d.ReceivedAt, &d.LastAttemptAt, &payload); err != nil {
			return nil, fmt.Errorf("pg store: scan due delivery: %w", err)
		}
		d.Payload = payload
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg store: iterate due deliveries: %w", err)
	}
	return out, nil
}

// CountByStatus implements Store.
func (s *PGStore) CountByStatus(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM ingest.deliveries GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("pg store: count by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("pg store: scan count: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// newPgUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505); used to map constraint collisions onto the domain
// sentinel without leaking driver types upward.
func newPgUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUnique {
		return err
	}
	return nil
}
