// Package seen implements ingest's replay seen-window (T6 residual-risk
// closure): a bounded in-memory LRU of delivery content classes with a TTL.
// A fresh-GUID delivery whose CONTENT class was already seen inside the
// window is flagged duplicate_suspect and forwarded record-only — never
// rejected (a hash collision must never drop valid-new traffic).
package seen

import (
	"container/list"
	"sync"
	"time"
)

// DefaultMaxEntries bounds the cache. WHY 100k: at ~100 bytes per entry this
// caps memory at ~10MB while covering days of webhook volume at storm rates
// (500 concurrent candidates × 8 repos); beyond that the oldest entries are
// evicted LRU-style and worst case we simply lose replay visibility, never
// correctness — exact-GUID dedupe remains the unique index's job.
const DefaultMaxEntries = 100000

// DefaultTTL is the documented near-replay window: duplicates within 24h are
// suspects; older repeats are ordinary traffic (GitHub redelivers for days).
const DefaultTTL = 24 * time.Hour

type entry struct {
	key   string
	stamp time.Time
}

// Cache is a concurrency-safe bounded LRU with per-entry expiry. The zero
// value is unusable; construct with New.
type Cache struct {
	mu      sync.Mutex
	max     int
	ttl     time.Duration
	nowFn   func() time.Time
	items   map[string]*list.Element
	order   *list.List // front = most recently used
	evicted int64
}

// New builds a Cache; maxEntries <= 0 uses DefaultMaxEntries, ttl <= 0 uses
// DefaultTTL, nowFn defaults to time.Now.
func New(maxEntries int, ttl time.Duration, nowFn func() time.Time) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Cache{
		max:   maxEntries,
		ttl:   ttl,
		nowFn: nowFn,
		items: make(map[string]*list.Element, maxEntries),
		order: list.New(),
	}
}

// SeenOrAdd reports whether key was already seen INSIDE the ttl window, then
// records/refreshes it atomically. true ⇒ duplicate suspect.
func (c *Cache) SeenOrAdd(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.nowFn()

	if el, ok := c.items[key]; ok {
		en := el.Value.(entry)
		if now.Sub(en.stamp) < c.ttl {
			// Refresh recency AND window so sustained replay pressure keeps
			// the class marked as seen.
			el.Value = entry{key: key, stamp: now}
			c.order.MoveToFront(el)
			return true
		}
		// Expired: fall through and re-insert as fresh.
		c.remove(el)
	}

	c.items[key] = c.order.PushFront(entry{key: key, stamp: now})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.remove(oldest)
	}
	return false
}

// Len returns the number of live entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// Evictions reports cumulative eviction count (tests/metrics).
func (c *Cache) Evictions() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evicted
}

func (c *Cache) remove(el *list.Element) {
	en := el.Value.(entry)
	delete(c.items, en.key)
	c.order.Remove(el)
	c.evicted++
}
