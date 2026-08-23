package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
)

// MemoryStore is an in-process Store used by tests and hermetic demos. It
// mirrors the uniqueness and retry-selection semantics of the Postgres
// implementation.
type MemoryStore struct {
	mu         sync.Mutex
	nowFn      func() time.Time
	deliveries map[string]domain.Delivery
}

// NewMemoryStore returns an empty store using the supplied clock.
func NewMemoryStore(nowFn func() time.Time) *MemoryStore {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &MemoryStore{nowFn: nowFn, deliveries: make(map[string]domain.Delivery)}
}

// InsertDelivery implements Store.
func (m *MemoryStore) InsertDelivery(_ context.Context, d domain.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := d.Source + "\x00" + d.ExtDeliveryID
	if _, ok := m.deliveries[key]; ok {
		return fmt.Errorf("memory store: insert delivery: %w", domain.ErrDuplicateDelivery)
	}
	m.deliveries[key] = d
	return nil
}

// GetDelivery implements Store.
func (m *MemoryStore) GetDelivery(_ context.Context, source, extDeliveryID string) (domain.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deliveries[source+"\x00"+extDeliveryID]
	if !ok {
		return domain.Delivery{}, fmt.Errorf("memory store: get delivery: %w", domain.ErrNotFound)
	}
	return d, nil
}

// UpdateForwardState implements Store.
func (m *MemoryStore) UpdateForwardState(_ context.Context, id string, status string, attempts int, lastAttemptAt time.Time, forwardedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.deliveries {
		if d.ID == id {
			d.Status = status
			d.Attempts = attempts
			d.LastAttemptAt = lastAttemptAt
			d.ForwardedAt = forwardedAt
			m.deliveries[k] = d
			return nil
		}
	}
	return fmt.Errorf("memory store: update forward state: %w", domain.ErrNotFound)
}

// DueDeliveries implements Store.
func (m *MemoryStore) DueDeliveries(_ context.Context, olderThan time.Duration, maxAttempts int, limit int) ([]domain.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.nowFn()
	var out []domain.Delivery
	for _, d := range m.deliveries {
		if d.Status == domain.StatusForwarded || d.Attempts >= maxAttempts {
			continue
		}
		cutoff := d.ReceivedAt
		if !d.LastAttemptAt.IsZero() {
			cutoff = d.LastAttemptAt
		}
		if now.Sub(cutoff) >= olderThan {
			out = append(out, d)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// CountByStatus implements Store.
func (m *MemoryStore) CountByStatus(_ context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := make(map[string]int64)
	for _, d := range m.deliveries {
		counts[d.Status]++
	}
	return counts, nil
}
