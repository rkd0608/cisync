package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"cisync.dev/cisync/github-connector/internal/queue"
)

// PGPendingQueue is the durable queue.Store over ghconn.pending_writes
// (migration 0003): budget-exhausted or GitHub-unavailable required-check
// writes survive restarts and drain later — never silently dropped (§4.6).
type PGPendingQueue struct {
	st *PGStore
}

// NewPendingQueue wires the durable outbox onto a connected PGStore.
func NewPendingQueue(st *PGStore) *PGPendingQueue { return &PGPendingQueue{st: st} }

// Enqueue implements queue.Store. Re-enqueueing an undelivered Key collapses
// via the partial unique index (memory-store parity); a delivered row with
// the same key may be re-enqueued as fresh work.
func (p *PGPendingQueue) Enqueue(ctx context.Context, w queue.PendingWrite) error {
	payload, err := json.Marshal(w.Payload)
	if err != nil {
		return fmt.Errorf("ghconn queue: marshal payload: %w", err)
	}
	id := w.ID
	if id == "" {
		id = newPendingWriteID()
	}
	createdAt := w.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	nextAttempt := w.NextAttemptAt
	if nextAttempt.IsZero() {
		nextAttempt = createdAt
	}
	_, err = p.st.pool.Exec(ctx, `
		INSERT INTO ghconn.pending_writes
		  (id, key, installation_id, repo, op, check_run_id, payload,
		   attempts, created_at, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (key) WHERE delivered_at IS NULL DO NOTHING`,
		id, w.Key, w.InstallationID, w.Repo, string(w.Op), w.CheckRunID,
		payload, w.Attempts, createdAt, nextAttempt)
	if err != nil {
		return fmt.Errorf("ghconn queue: enqueue %s: %w", w.Key, err)
	}
	return nil
}

// Due implements queue.Store: undelivered writes with NextAttemptAt <= now,
// FIFO by creation, capped at limit.
func (p *PGPendingQueue) Due(ctx context.Context, now time.Time, limit int) ([]queue.PendingWrite, error) {
	rows, err := p.st.pool.Query(ctx, `
		SELECT id, key, installation_id, repo, op, check_run_id, payload,
		       attempts, created_at, next_attempt_at
		FROM ghconn.pending_writes
		WHERE delivered_at IS NULL AND next_attempt_at <= $1
		ORDER BY created_at ASC, id ASC
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("ghconn queue: due query: %w", err)
	}
	defer rows.Close()
	var out []queue.PendingWrite
	for rows.Next() {
		var w queue.PendingWrite
		var op string
		var payload []byte
		if err := rows.Scan(&w.ID, &w.Key, &w.InstallationID, &w.Repo, &op,
			&w.CheckRunID, &payload, &w.Attempts, &w.CreatedAt, &w.NextAttemptAt); err != nil {
			return nil, fmt.Errorf("ghconn queue: due scan: %w", err)
		}
		w.Op = queue.Op(op)
		if err := json.Unmarshal(payload, &w.Payload); err != nil {
			return nil, fmt.Errorf("ghconn queue: due payload: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkDelivered implements queue.Store: unknown ids are a no-op so at-least-
// once drainers and restarts converge without errors.
func (p *PGPendingQueue) MarkDelivered(ctx context.Context, id string, at time.Time) error {
	_, err := p.st.pool.Exec(ctx,
		`UPDATE ghconn.pending_writes SET delivered_at=$2 WHERE id=$1 AND delivered_at IS NULL`,
		id, at)
	if err != nil {
		return fmt.Errorf("ghconn queue: mark delivered %s: %w", id, err)
	}
	return nil
}

// Reschedule implements queue.Store: exponential-backoff bookkeeping for the
// drainer's retry clock.
func (p *PGPendingQueue) Reschedule(ctx context.Context, id string, next time.Time, attempts int) error {
	_, err := p.st.pool.Exec(ctx,
		`UPDATE ghconn.pending_writes SET next_attempt_at=$2, attempts=$3 WHERE id=$1`,
		id, next, attempts)
	if err != nil {
		return fmt.Errorf("ghconn queue: reschedule %s: %w", id, err)
	}
	return nil
}

// newPendingWriteID mints a collision-resistant row id without adding
// dependencies: nanosecond timestamp + 8 random bytes.
func newPendingWriteID() string {
	var random [8]byte
	_, _ = rand.Read(random[:])
	return "pw_" + fmt.Sprintf("%016x", time.Now().UnixNano()) + "_" + hex.EncodeToString(random[:])
}
