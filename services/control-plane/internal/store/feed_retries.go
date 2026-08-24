package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MaxFeedAttempts bounds how many ticks a poison completion row may fail
// before it is absorbed as a processed diagnostic (P1-4). The feed
// re-presents accepted rows every tick, so the cap alone bounds total work;
// no exponential timer is needed at v1 cadence.
const MaxFeedAttempts = 5

// RecordFeedFailureTx counts one failed processing attempt for a dedupe key.
func RecordFeedFailureTx(ctx context.Context, tx pgx.Tx, consumer, eventKey string) (int, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO ctrl.feed_retries (consumer, event_key, attempts, updated_at)
		 VALUES ($1,$2,1,now())
		 ON CONFLICT (consumer, event_key)
		 DO UPDATE SET attempts = ctrl.feed_retries.attempts + 1, updated_at = now()`,
		consumer, eventKey)
	if err != nil {
		return 0, fmt.Errorf("record feed failure: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// FeedFailureCount reads the current consecutive-failure count for a key.
func (s *Store) FeedFailureCount(ctx context.Context, consumer, eventKey string) (int, error) {
	var attempts int
	err := s.Pool.QueryRow(ctx,
		`SELECT attempts FROM ctrl.feed_retries WHERE consumer=$1 AND event_key=$2`,
		consumer, eventKey).Scan(&attempts)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("feed failure count: %w", err)
	}
	return attempts, nil
}

// ClearFeedRetriesTx drops retry bookkeeping once a key applies cleanly or
// is absorbed permanently.
func ClearFeedRetriesTx(ctx context.Context, tx pgx.Tx, consumer string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM ctrl.feed_retries WHERE consumer=$1 AND event_key = ANY($2)`,
		consumer, keys); err != nil {
		return fmt.Errorf("clear feed retries: %w", err)
	}
	return nil
}
