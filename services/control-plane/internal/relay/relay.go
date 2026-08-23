package relay

import (
	"context"
	"time"

	"sauron.dev/sauron/control-plane/internal/store"
)

// Relay drains ctrl.outbox to in-process consumers using SKIP LOCKED batches,
// woken by NOTIFY 'outbox_changed' with a 500ms poll fallback.
type Relay struct {
	store    *store.Store
	batch    int
	poll     time.Duration
	handlers map[string][]Handler
}

// Handler processes one delivered outbox event; errors trigger backoff.
type Handler func(ctx context.Context, item store.OutboxItem) error

// New constructs a Relay.
func New(st *store.Store, batch int, poll time.Duration) *Relay {
	if batch <= 0 {
		batch = 100
	}
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return &Relay{store: st, batch: batch, poll: poll, handlers: map[string][]Handler{}}
}

// Register attaches a handler for an event type prefix ("" = all).
func (r *Relay) Register(eventType string, h Handler) {
	r.handlers[eventType] = append(r.handlers[eventType], h)
}

// Run blocks until ctx is cancelled, draining the outbox forever.
func (r *Relay) Run(ctx context.Context) {
	go r.listenNotify(ctx)
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		if err := r.drainOnce(ctx); err != nil && ctx.Err() == nil {
			logf("relay drain: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// listenNotify subscribes to outbox_changed notifications as a wake hint;
// polling remains the correctness fallback.
func (r *Relay) listenNotify(ctx context.Context) {
	conn, err := r.store.Pool.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN outbox_changed"); err != nil {
		return
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if n != nil && n.Channel == "outbox_changed" {
			if err := r.drainOnce(ctx); err != nil && ctx.Err() == nil {
				logf("relay notify drain: %v", err)
			}
		}
	}
}

func (r *Relay) drainOnce(ctx context.Context) error {
	items, err := r.store.ClaimOutboxBatch(ctx, r.batch)
	if err != nil {
		return err
	}
	for _, item := range items {
		var failure error
		for prefix, hs := range r.handlers {
			if prefix != "" && prefix != item.Type {
				continue
			}
			for _, h := range hs {
				if err := h(ctx, item); err != nil {
					failure = err
					break
				}
			}
		}
		if failure != nil {
			if ferr := r.store.MarkFailed(ctx, item.ID, item.Attempts); ferr != nil {
				return ferr
			}
			continue
		}
		if perr := r.store.MarkPublished(ctx, item.ID); perr != nil {
			return perr
		}
	}
	return nil
}
