package relay

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// Dispatcher is the v1 Scheduler stub: Tick scans queued runs and dispatches
// them to runner-fleet via the claim API (internal-protocols §2).
type Dispatcher struct {
	store *store.Store
	fleet *FleetClient
	pool  string
	batch int
}

// NewDispatcher constructs the scheduler stub.
func NewDispatcher(st *store.Store, fleet *FleetClient, pool string, batch int) *Dispatcher {
	if batch <= 0 {
		batch = 8
	}
	return &Dispatcher{store: st, fleet: fleet, pool: pool, batch: batch}
}

// Tick implements domain.Scheduler: admit + dispatch up to batch queued runs.
// Runs the fleet did not echo back stay queued for the next tick.
func (d *Dispatcher) Tick(ctx context.Context) (int, error) {
	queued, err := d.store.QueuedRuns(ctx, d.batch)
	if err != nil {
		return 0, err
	}
	if len(queued) == 0 {
		return 0, nil
	}
	jobs, err := d.fleet.Claim(ctx, d.pool, len(queued))
	if err != nil {
		return 0, err
	}
	claimed := make(map[string]ClaimJob, len(jobs))
	for _, j := range jobs {
		claimed[j.RunID] = j
	}
	dispatched := 0
	for _, qr := range queued {
		job, ok := claimed[qr.ID]
		if !ok {
			continue
		}
		run, err := d.store.GetRun(ctx, qr.TenantID, qr.ID)
		if err != nil {
			logf("load run %s: %v", qr.ID, err)
			continue
		}
		run.Attempt = job.Attempt
		run.FenceToken = job.FenceToken
		err = d.store.ExecTx(ctx, func(tx pgx.Tx) error {
			_, err := store.DispatchRunTx(ctx, tx, d.store, run)
			return err
		})
		if err != nil {
			logf("dispatch %s: %v", qr.ID, err)
			continue
		}
		dispatched++
	}
	return dispatched, nil
}

// ConsumeRequested is the relay consumer for validation.requested events: it
// dedupes via processed_events inside the effect tx (invariant I-12), then
// lets the next Tick pick up the queued run.
func (d *Dispatcher) ConsumeRequested(ctx context.Context, item store.OutboxItem) error {
	return d.store.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := store.MarkProcessedTx(ctx, tx, "scheduler", item.EventID)
		return err
	})
}

// Run loops Tick until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := d.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			logf("tick: %v", err)
		}
		if n > 0 {
			logf("dispatched %d run(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

var _ domain.Scheduler = (*Dispatcher)(nil)
