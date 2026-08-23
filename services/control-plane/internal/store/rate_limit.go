package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TakeToken consumes one token from a refill-per-minute token bucket,
// creating it on first use. ok=false carries the suggested retry delay.
func (s *Store) TakeToken(ctx context.Context, tenantID, bucket string, capacity, perMinute int) (bool, time.Duration, error) {
	refillPerSec := float64(perMinute) / 60.0
	now := time.Now().UTC()
	var granted bool
	var retryAfter time.Duration
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var tokens float64
		var lastRefill time.Time
		err := tx.QueryRow(ctx,
			`SELECT tokens, last_refill FROM ctrl.rate_limits WHERE tenant_id=$1 AND bucket=$2`,
			tenantID, bucket).Scan(&tokens, &lastRefill)
		if err != nil {
			tokens = float64(capacity)
			lastRefill = now
			_, ierr := tx.Exec(ctx,
				`INSERT INTO ctrl.rate_limits (tenant_id, bucket, tokens, last_refill) VALUES ($1,$2,$3,$4)
				 ON CONFLICT (tenant_id, bucket) DO NOTHING`,
				tenantID, bucket, tokens, now)
			if ierr != nil {
				return fmt.Errorf("rate bucket init: %w", ierr)
			}
		} else {
			elapsed := now.Sub(lastRefill).Seconds()
			tokens += elapsed * refillPerSec
			if tokens > float64(capacity) {
				tokens = float64(capacity)
			}
		}
		if tokens >= 1 {
			granted = true
			tokens -= 1
			retryAfter = 0
		} else {
			retryAfter = time.Duration((1 - tokens) / refillPerSec * float64(time.Second))
		}
		_, uerr := tx.Exec(ctx,
			`UPDATE ctrl.rate_limits SET tokens=$3, last_refill=$4 WHERE tenant_id=$1 AND bucket=$2`,
			tenantID, bucket, tokens, now)
		return uerr
	})
	if err != nil {
		return false, 0, fmt.Errorf("take token: %w", err)
	}
	return granted, retryAfter, nil
}
