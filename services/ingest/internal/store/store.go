// Package store defines the persistence contract for ingest deliveries. Per
// ARCHITECTURE D1 Postgres is the sole state authority; the interface exists so
// alternate engines and test doubles can slot in.
package store

import (
	"context"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
)

// Store persists raw webhook deliveries (audit anchor, T6) and their forward
// state. Implementations must enforce uniqueness of (source, ext_delivery_id).
type Store interface {
	// InsertDelivery persists a new delivery; ErrDuplicateDelivery when the
	// (source, ext_delivery_id) pair already exists.
	InsertDelivery(ctx context.Context, d domain.Delivery) error
	// GetDelivery fetches one delivery by (source, ext_delivery_id).
	GetDelivery(ctx context.Context, source, extDeliveryID string) (domain.Delivery, error)
	// UpdateForwardState records the outcome of a forward attempt.
	UpdateForwardState(ctx context.Context, id string, status string, attempts int, lastAttemptAt time.Time, forwardedAt time.Time) error
	// DueDeliveries returns non-forwarded deliveries eligible for retry: last
	// attempt older than olderThan and attempts below maxAttempts.
	DueDeliveries(ctx context.Context, olderThan time.Duration, maxAttempts int, limit int) ([]domain.Delivery, error)
	// CountByStatus returns the number of deliveries per status (metrics).
	CountByStatus(ctx context.Context) (map[string]int64, error)
}
