package relay

import (
	"context"
	"time"

	"sauron.dev/sauron/control-plane/internal/store"
)

// Reconciler expires TTL-passed leases, cancels stale dispatched runs, and
// enforces security-audit retention (THREAT_MODEL B7 >=90d policy).
type Reconciler struct {
	store *store.Store
	fleet *FleetClient
	// staleMaxAge bounds dispatched-without-completion lifetime; see
	// config.StaleRunMaxAge for why it is configurable.
	staleMaxAge time.Duration
	// auditRetention bounds ctrl.security_audit row age; <=0 falls back to
	// DefaultAuditRetentionDays. Pruning keeps the audit table bounded
	// while satisfying the B7 minimum retention window.
	auditRetention time.Duration
}

// DefaultStaleRunMaxAge is the documented prod posture: 2× the default job
// timeout (15 min).
const DefaultStaleRunMaxAge = 30 * time.Minute

// DefaultAuditRetentionDays is the B7 retention floor: 90 days of
// security-audit rows before the reconciler prunes them.
const DefaultAuditRetentionDays = 90

// NewReconciler constructs the reconciler; maxAge ≤ 0 falls back to the
// documented 30-minute posture, retentionDays ≤ 0 to the 90-day floor.
func NewReconciler(st *store.Store, fleet *FleetClient, maxAge time.Duration, retentionDays int) *Reconciler {
	if maxAge <= 0 {
		maxAge = DefaultStaleRunMaxAge
	}
	retention := time.Duration(retentionDays) * 24 * time.Hour
	if retentionDays <= 0 {
		retention = DefaultAuditRetentionDays * 24 * time.Hour
	}
	return &Reconciler{store: st, fleet: fleet, staleMaxAge: maxAge, auditRetention: retention}
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

// ReconcileOnce expires due leases (lease.expired + budget release), cancels
// dispatched runs older than 2× their timeout fencing them at fleet, and
// prunes security-audit rows beyond the retention horizon.
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

	cancelled, err := rc.store.CancelStaleDispatchedRuns(ctx, rc.staleMaxAge, "stale_base")
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

	pruned, err := rc.store.PruneSecurityAudit(ctx, time.Now().UTC().Add(-rc.auditRetention))
	if err != nil {
		return err
	}
	if pruned > 0 {
		logf("pruned %d security-audit row(s) older than %sd", pruned, rc.auditRetention.Hours()/24)
	}
	return nil
}
