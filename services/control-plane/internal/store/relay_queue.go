package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OutboxItem is one undelivered outbox row.
type OutboxItem struct {
	ID       int64
	EventID  string
	TenantID string
	Type     string
	AggType  string
	AggID    string
	Attempts int
}

// ClaimOutboxBatch selects up to batch pending rows with FOR UPDATE SKIP
// LOCKED, marking them 'delivering' so concurrent relays never double-send.
func (s *Store) ClaimOutboxBatch(ctx context.Context, batch int) ([]OutboxItem, error) {
	items := []OutboxItem{}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, event_id, tenant_id, type, aggregate_type, aggregate_id, attempts
			 FROM ctrl.outbox WHERE status='pending' AND next_attempt_at <= now()
			 ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, batch)
		if err != nil {
			return fmt.Errorf("outbox claim: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var it OutboxItem
			if err := rows.Scan(&it.ID, &it.EventID, &it.TenantID, &it.Type, &it.AggType, &it.AggID, &it.Attempts); err != nil {
				return fmt.Errorf("outbox claim scan: %w", err)
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range items {
			if _, err := tx.Exec(ctx,
				`UPDATE ctrl.outbox SET status='delivering', attempts=attempts+1 WHERE id=$1`, items[i].ID); err != nil {
				return fmt.Errorf("outbox claim mark: %w", err)
			}
			items[i].Attempts++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// MarkPublished finalizes delivery of one claimed row.
func (s *Store) MarkPublished(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE ctrl.outbox SET status='published', published_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("outbox publish: %w", err)
	}
	return nil
}

// MarkFailed returns a row to pending with backoff; after maxAttempts it is
// parked as failed for operator triage.
func (s *Store) MarkFailed(ctx context.Context, id int64, attempts int) error {
	status := "pending"
	next := time.Now().UTC().Add(time.Duration(attempts) * time.Second)
	if attempts >= 10 {
		status = "failed"
	}
	_, err := s.Pool.Exec(ctx,
		`UPDATE ctrl.outbox SET status=$2, next_attempt_at=$3 WHERE id=$1`, id, status, next)
	if err != nil {
		return fmt.Errorf("outbox fail: %w", err)
	}
	return nil
}

// ReclaimStuckDelivering re-arms rows stuck in delivering (crash recovery).
func (s *Store) ReclaimStuckDelivering(ctx context.Context, olderThan time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE ctrl.outbox SET status='pending' WHERE status='delivering' AND created_at < $1`,
		time.Now().UTC().Add(-olderThan))
	if err != nil {
		return fmt.Errorf("outbox reclaim: %w", err)
	}
	return nil
}

// OutboxDepth reports pending+delivering row count (backpressure gauge).
func (s *Store) OutboxDepth(ctx context.Context) (int64, error) {
	var depth int64
	err := s.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ctrl.outbox WHERE status IN ('pending','delivering')`).Scan(&depth)
	if err != nil {
		return 0, fmt.Errorf("outbox depth: %w", err)
	}
	return depth, nil
}
