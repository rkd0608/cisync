package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/domain"
)

// GetLease loads a lease within tenant (I-14).
func (s *Store) GetLease(ctx context.Context, tenantID, leaseID string) (*domain.Lease, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, tenant_id, intent_id, state, scope_kind, surfaces, env_template, holder,
		        budget, ttl_expires_at, renewal_count, queue_position, eta_seconds,
		        required_evidence, created_at, released_at
		 FROM ctrl.leases WHERE id=$1 AND tenant_id=$2`, leaseID, tenantID)
	var l domain.Lease
	var scopeKind string
	var envTemplate *string
	var budgetRaw, reqRaw []byte
	var releasedAt *time.Time
	err := row.Scan(&l.ID, &l.TenantID, &l.IntentID, &l.State, &scopeKind, &l.Scope.Surfaces,
		&envTemplate, &l.Holder, &budgetRaw, &l.TTLExpiresAt, &l.RenewalCount,
		&l.QueuePosition, &l.EtaSeconds, &reqRaw, &l.CreatedAt, &releasedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get lease: %w", err)
	}
	l.Scope.Kind = domain.LeaseScopeKind(scopeKind)
	if envTemplate != nil {
		l.Scope.EnvTemplate = *envTemplate
	}
	l.ReleasedAt = releasedAt
	return &l, nil
}

// RenewLease persists a renewed TTL with ONE conditional UPDATE: the row
// must still be granted AND its current TTL must not have expired (checked
// in-tx against now()), so two racing renews serialize on the row lock and
// the post-check instead of a read-mutate-write window (P1-3). RowsAffected
// == 0 ⇒ unknown lease OR post-terminal/expired state, surfaced as typed
// ErrConflict. Ledger event + projection commit in the SAME tx.
func (s *Store) RenewLease(ctx context.Context, tenantID, leaseID string, ttlSeconds int64) (time.Time, int, error) {
	corr := domain.NewCorrelationID()
	var newTTL time.Time
	var renewalCount int
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE ctrl.leases SET
			    ttl_expires_at = now() + make_interval(secs => $3),
			    renewal_count = renewal_count + 1
			 WHERE id=$1 AND tenant_id=$2 AND state='granted' AND ttl_expires_at > now()`,
			leaseID, tenantID, ttlSeconds)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		if err := tx.QueryRow(ctx,
			`SELECT ttl_expires_at, renewal_count FROM ctrl.leases WHERE id=$1 AND tenant_id=$2`,
			leaseID, tenantID,
		).Scan(&newTTL, &renewalCount); err != nil {
			return err
		}
		actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "control-plane"}
		ev, err := domain.NewEvent(tenantID,
			domain.AggregateRef{Type: string(domain.AggLease), ID: leaseID},
			"lease.renewed", "", corr, actor, map[string]any{
				"lease_id":       leaseID,
				"ttl_expires_at": newTTL.Format(time.RFC3339),
				"renewal_count":  renewalCount,
			})
		if err != nil {
			return err
		}
		if err := s.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE ctrl.leases SET seq=$3 WHERE id=$1 AND tenant_id=$2`,
			leaseID, tenantID, ev.Seq)
		return err
	})
	if err != nil {
		return time.Time{}, 0, err
	}
	return newTTL, renewalCount, nil
}

// ReleaseLease marks a granted lease released (idempotent); standalone form
// used by the reconciler. Callers inside an open effect tx must use
// ReleaseLeaseTx — a nested transaction self-deadlocks on the append lock.
func (s *Store) ReleaseLease(ctx context.Context, l *domain.Lease, reason string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return releaseLeaseTx(ctx, s, tx, l, reason)
	})
}

// ReleaseLeaseTx is ReleaseLease within a caller-supplied transaction.
func (s *Store) ReleaseLeaseTx(ctx context.Context, tx pgx.Tx, l *domain.Lease, reason string) error {
	return releaseLeaseTx(ctx, s, tx, l, reason)
}

func releaseLeaseTx(ctx context.Context, s *Store, tx pgx.Tx, l *domain.Lease, reason string) error {
	eventType := "lease.revoked"
	if reason == "ttl" {
		eventType = "lease.expired"
	}
	corr := domain.NewCorrelationID()
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "control-plane"}
	ev, err := domain.NewEvent(l.TenantID,
		domain.AggregateRef{Type: string(domain.AggLease), ID: l.ID},
		eventType, "", corr, actor, map[string]any{
			"lease_id":        l.ID,
			"reason":          reason,
			"released_budget": l.Budget,
		})
	if err != nil {
		return err
	}
	if err := s.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return err
	}
	// B7: teardown-class revocations are security-audit events. Emission
	// rides the SAME tx as lease.revoked so the audit trail can never show
	// fewer teardowns than the ledger.
	if _, tornDown := TeardownLeaseReasons[reason]; tornDown {
		aev, aerr := audit.New(l.TenantID, audit.KindLeaseRevocation,
			audit.Actor{Kind: string(domain.ActorSystem), ID: "control-plane"},
			map[string]any{"lease_id": l.ID, "intent_id": l.IntentID},
			map[string]any{"reason": reason})
		if aerr != nil {
			return aerr
		}
		if err := insertSecurityAuditTx(ctx, tx, aev); err != nil {
			return err
		}
		s.notifyAudit(audit.KindLeaseRevocation)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.leases SET state=$3, seq=$4, released_at=now() WHERE id=$1 AND tenant_id=$2 AND state='granted'`,
		l.ID, l.TenantID, string(l.State), ev.Seq)
	if err != nil {
		return err
	}
	_ = tag
	return nil
}

// LeaseForIntent returns active change-scope leases for an intent.
func (s *Store) LeaseForIntent(ctx context.Context, tenantID, intentID string) ([]*domain.Lease, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id FROM ctrl.leases WHERE tenant_id=$1 AND intent_id=$2 AND state='granted'
		 ORDER BY created_at DESC LIMIT 5`, tenantID, intentID)
	if err != nil {
		return nil, fmt.Errorf("lease for intent: %w", err)
	}
	defer rows.Close()
	var out []*domain.Lease
	for rows.Next() {
		l := &domain.Lease{IntentID: intentID, TenantID: tenantID}
		if err := rows.Scan(&l.ID); err != nil {
			return nil, fmt.Errorf("scan lease id: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DueLeases returns granted leases whose TTL has passed.
func (s *Store) DueLeases(ctx context.Context, now time.Time) ([]*domain.Lease, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, tenant_id, intent_id FROM ctrl.leases WHERE state='granted' AND ttl_expires_at < $1 LIMIT 100`,
		now)
	if err != nil {
		return nil, fmt.Errorf("due leases: %w", err)
	}
	defer rows.Close()
	var out []*domain.Lease
	for rows.Next() {
		l := &domain.Lease{}
		if err := rows.Scan(&l.ID, &l.TenantID, &l.IntentID); err != nil {
			return nil, fmt.Errorf("scan due lease: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
