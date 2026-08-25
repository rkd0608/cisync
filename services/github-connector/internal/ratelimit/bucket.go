// Package ratelimit is the connector-local write budget: one token bucket
// per installation (plan §4.6) so a busy tenant can never starve another,
// plus retry classification for GitHub rate-limit/5xx responses.
package ratelimit

import (
	"sync"
	"time"
)

// Bucket is a continuously-refilling token bucket sized
// capacity = perHour, refill rate = perHour/hour.
type Bucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	last     time.Time
	now      func() time.Time
}

// NewBucket builds a bucket with perHour tokens of hourly refill.
func NewBucket(perHour int, now func() time.Time) *Bucket {
	if now == nil {
		now = time.Now
	}
	return &Bucket{
		tokens:   float64(perHour),
		capacity: float64(perHour),
		// WHY continuous refill (not hourly reset): steady 3-writes-per-
		// revision traffic should never see burst starvation mid-hour.
		rate: float64(perHour) / float64(time.Hour/time.Second),
		last: now(),
		now:  now,
	}
}

// TryAcquire consumes one token if available and reports success.
func (b *Bucket) TryAcquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RetryIn reports how long until the next token is available; zero when a
// token is immediately available. Used to schedule pending-write retries.
func (b *Bucket) RetryIn() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens >= 1 {
		return 0
	}
	// WHY the zero-rate guard: a 0/hour budget (hard freeze) would divide
	// by zero below; report an hour as a sane "check back later" interval.
	if b.rate <= 0 {
		return time.Hour
	}
	need := (1 - b.tokens) / b.rate
	return time.Duration(need * float64(time.Second))
}

// refill advances tokens by elapsed time; caller holds mu.
func (b *Bucket) refill() {
	now := b.now()
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed.Seconds() * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
}

// Budget multiplexes one bucket per known installation.
type Budget struct {
	mu      sync.Mutex
	perHour int
	now     func() time.Time
	buckets map[int64]*Bucket
}

// NewBudget builds the per-installation budget (default 300 writes/h).
func NewBudget(perHour int, now func() time.Time) *Budget {
	return &Budget{perHour: perHour, now: now, buckets: make(map[int64]*Bucket)}
}

// TryAcquire consumes a write token for the installation, lazily creating
// its bucket on first sight.
func (g *Budget) TryAcquire(installationID int64) bool {
	g.mu.Lock()
	b, ok := g.buckets[installationID]
	if !ok {
		b = NewBucket(g.perHour, g.now)
		g.buckets[installationID] = b
	}
	g.mu.Unlock()
	return b.TryAcquire()
}

// RetryIn reports the wait for the installation's next token.
func (g *Budget) RetryIn(installationID int64) time.Duration {
	g.mu.Lock()
	b, ok := g.buckets[installationID]
	g.mu.Unlock()
	if !ok {
		return 0
	}
	return b.RetryIn()
}
