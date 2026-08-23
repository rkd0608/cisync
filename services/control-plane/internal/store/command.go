package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// CachedCommand is a previously stored idempotent response.
type CachedCommand struct {
	ResponseCode int
	ResponseBody []byte
}

// LookupCommand returns the cached response for (tenant, endpoint, key) when
// present and the request hash matches; a hash mismatch is a 409-grade
// version_conflict surfaced as ErrConflict by callers.
func (s *Store) LookupCommand(ctx context.Context, tenantID, endpoint, key, requestHash string) (*CachedCommand, error) {
	var code int
	var body []byte
	var hash string
	err := s.Pool.QueryRow(ctx,
		`SELECT request_hash, response_code, response_body FROM ctrl.command_log
		 WHERE tenant_id=$1 AND endpoint=$2 AND command_key=$3`,
		tenantID, endpoint, key,
	).Scan(&hash, &code, &body)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup command: %w", err)
	}
	if hash != requestHash {
		return &CachedCommand{}, domain.ErrConflict
	}
	return &CachedCommand{ResponseCode: code, ResponseBody: body}, nil
}

// RecordCommand stores an executed response inside the caller's transaction.
func RecordCommandTx(ctx context.Context, tx pgx.Tx, tenantID, endpoint, key, requestHash string, code int, body []byte) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO ctrl.command_log (tenant_id, endpoint, command_key, request_hash, response_code, response_body)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tenant_id, endpoint, command_key) DO NOTHING`,
		tenantID, endpoint, key, requestHash, code, body,
	); err != nil {
		return fmt.Errorf("record command: %w", err)
	}
	return nil
}

// MarkProcessedTx records event consumption inside an effect transaction;
// returning false means the event was already applied and must be skipped
// (invariant I-12).
func MarkProcessedTx(ctx context.Context, tx pgx.Tx, consumer, eventID string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO ctrl.processed_events (event_id, consumer) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		eventID, consumer,
	)
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ProcessedKeys bulk-checks which of the given dedupe keys the consumer has
// already applied. WHY a pre-check: the fleet completion feed (§4) replays
// accepted rows forever by contract, so every tick would otherwise run the
// full load+tx pipeline for already-consumed rows just to hit the I-12
// dedupe inside it. This read is advisory only — MarkProcessedTx inside the
// effect tx remains the authoritative race-safe gate.
func (s *Store) ProcessedKeys(ctx context.Context, consumer string, keys []string) (map[string]bool, error) {
	found := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return found, nil
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT event_id FROM ctrl.processed_events WHERE consumer=$1 AND event_id = ANY($2)`,
		consumer, keys)
	if err != nil {
		return nil, fmt.Errorf("processed keys lookup: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan processed key: %w", err)
		}
		found[key] = true
	}
	return found, rows.Err()
}
