package queue

import (
	"context"
	"log/slog"
	"time"

	"cisync.dev/cisync/github-connector/internal/ratelimit"
)

const (
	// maxAttempts bounds per-write retries before the sweeper-visible depth
	// gauge keeps screaming; a required check never leaves this queue
	// silently (plan §4.6).
	maxAttempts = 8
	// backoffBase/backoffCap bound the exponential retry curve for API
	// failures; budget-exhausted writes wait on RetryIn instead.
	backoffBase = 2 * time.Second
	backoffCap  = 15 * time.Minute
)

// Deliver publishes one buffered write through the live publishers; wired
// by the server assembly (repo → installation client resolution included).
type Deliver func(ctx context.Context, w PendingWrite) error

// Drainer periodically flushes due pending writes through the rate gate.
type Drainer struct {
	store    Store
	gate     *ratelimit.Gate
	budget   *ratelimit.Budget
	deliver  Deliver
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger
	onDepth  func(depth int)
}

// NewDrainer wires the drainer. interval and now are injectable for tests;
// onDepth (optional) feeds the pending-depth gauge each tick.
func NewDrainer(store Store, gate *ratelimit.Gate, budget *ratelimit.Budget, deliver Deliver, interval time.Duration, now func() time.Time, logger *slog.Logger, onDepth func(int)) *Drainer {
	if now == nil {
		now = time.Now
	}
	return &Drainer{
		store: store, gate: gate, budget: budget, deliver: deliver,
		interval: interval, now: now, logger: logger, onDepth: onDepth,
	}
}

// Run blocks until ctx is cancelled, draining each tick. Intended as its
// own goroutine next to the HTTP server loop.
func (d *Drainer) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

// Tick performs exactly one drain pass (exported for test harnesses that
// drive time manually).
func (d *Drainer) Tick(ctx context.Context) { d.tick(ctx) }

func (d *Drainer) tick(ctx context.Context) {
	due, err := d.store.Due(ctx, d.now(), 25)
	if err != nil {
		d.logger.Error("pending-write scan failed", slog.String("err", err.Error()))
		return
	}
	for _, w := range due {
		d.deliverOne(ctx, w)
	}
	if d.onDepth != nil {
		if counter, ok := d.store.(*MemoryStore); ok {
			d.onDepth(counter.Depth())
		}
	}
}

func (d *Drainer) deliverOne(ctx context.Context, w PendingWrite) {
	err := d.gate.Do(ctx, w.InstallationID, func(callCtx context.Context) error {
		return d.deliver(callCtx, w)
	})
	switch {
	case err == nil:
		if markErr := d.store.MarkDelivered(ctx, w.ID, d.now()); markErr != nil {
			d.logger.Error("pending-write completion persist failed", slog.String("id", w.ID), slog.String("err", markErr.Error()))
		}
	case isBudgetExhausted(err):
		wait := d.budget.RetryIn(w.InstallationID)
		if wait <= 0 {
			wait = time.Minute
		}
		_ = d.store.Reschedule(ctx, w.ID, d.now().Add(wait), w.Attempts)
	default:
		attempts := w.Attempts + 1
		next := d.now().Add(backoffForAttempt(attempts))
		_ = d.store.Reschedule(ctx, w.ID, next, attempts)
		d.logger.Warn("pending write failed; rescheduled",
			slog.String("id", w.ID), slog.Int("attempts", attempts),
			slog.Time("next", next), slog.String("err", err.Error()))
	}
}

func isBudgetExhausted(err error) bool {
	return err == ratelimit.ErrBudgetExhausted
}

func backoffForAttempt(attempt int) time.Duration {
	backoff := backoffBase << attempt
	if backoff > backoffCap || backoff <= 0 {
		return backoffCap
	}
	return backoff
}
