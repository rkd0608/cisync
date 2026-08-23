package relay

import (
	"context"
	"time"

	"sauron.dev/sauron/control-plane/internal/store"
)

// Reconciler expires TTL-passed leases and cancels stale dispatched runs.
type Reconciler struct {
	store *store.Store
	fleet *FleetClient
}

// NewReconciler constructs the reconciler.
func NewReconciler(st *store.Store, fleet *FleetClient) *Reconciler {
	return &Reconciler{store: st, fleet: fleet}
}

// Run loops reconcile every interval until ctx is cancelled (ARCHITECTURE §3:
// 30s cadence).
func (rc *Reconciler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := rc.ReconcileOnce(ctx); err != nil {
				logf("reconcile: %v", err)
			}
		}
	}
}

// ReconcileOnce expires due leases (lease.expired + budget release) and
// cancels dispatched runs older than 2× their timeout, fencing them at fleet.
func (rc *Reconciler) ReconcileOnce(ctx context.Context) error {
	due, err := rc.store.DueLeases(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, l := range due {
		full, err := rc.store.GetLease(ctx, l.TenantID, l.ID)
		if err != nil {
			logf("load lease %s: %v", l.ID, err)
			continue
		}
		if !full.Expired(time.Now().UTC()) {
			continue
		}
		if err := full.Apply("lease.expired"); err != nil {
			continue
		}
		if err := rc.store.ReleaseLease(ctx, full, "ttl"); err != nil {
			logf("expire lease %s: %v", l.ID, err)
		} else {
			logf("expired lease %s", l.ID)
		}
	}

	staleMaxAge := 2 * 15 * time.Minute
	cancelled, err := rc.store.CancelStaleDispatchedRuns(ctx, staleMaxAge, "stale_base")
	if err != nil {
		return err
	}
	for _, runID := range cancelled {
		if rc.fleet != nil {
			if cerr := rc.fleet.Cancel(ctx, runID, "superseded"); cerr != nil {
				logf("fence cancel %s: %v", runID, cerr)
			}
		}
		logf("cancelled stale run %s", runID)
	}
	return nil
}
