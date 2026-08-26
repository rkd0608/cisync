package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/audit"
)

// TeardownLeaseReasons are the lease.revocation reasons that count as
// administrative teardown (THREAT_MODEL B7 audit point "lease revocations
// reason=tenant_teardown"). Routine business-as-usual revocations
// (superseded/intent_terminal) and TTL expiry stay out of the security
// stream; tenant_teardown is the events.schema.json-designated reason and
// repo_deleted is its live G10 (installation.deleted) sibling — auditing both
// means every future teardown producer is covered by the same filter.
var TeardownLeaseReasons = map[string]struct{}{
	"tenant_teardown": {},
	"repo_deleted":    {},
}

// InsertSecurityAudit persists one audit row on the pool. Use the Tx form
// whenever the triggering fact commits inside a transaction.
func (s *Store) InsertSecurityAudit(ctx context.Context, ev audit.Event) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return insertSecurityAuditTx(ctx, tx, ev)
	})
}

// InsertSecurityAuditTx persists one audit row inside the caller's
// transaction so the audit fact is atomic with the event it witnesses
// ("same tx where possible" per B7): a rolled-back rejection never leaves an
// orphan audit row and a committed one always does.
func (s *Store) InsertSecurityAuditTx(ctx context.Context, tx pgx.Tx, ev audit.Event) error {
	return insertSecurityAuditTx(ctx, tx, ev)
}

func insertSecurityAuditTx(ctx context.Context, tx pgx.Tx, ev audit.Event) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO ctrl.security_audit (id, ts, tenant_id, event_kind, actor, subject, detail)
		 VALUES ($1, now(), $2, $3, $4::jsonb, $5::jsonb, $6::jsonb)`,
		ev.ID, ev.TenantID, string(ev.Kind), actorJSON(ev.Actor), ev.Subject, ev.Detail)
	if err != nil {
		return fmt.Errorf("insert security audit: %w", err)
	}
	return nil
}

// notifyAudit fires the optional metric observer (never blocks emission:
// a panicking hook is out of contract, so it stays trivially synchronous).
func (s *Store) notifyAudit(kind audit.Kind) {
	if s.AuditObserver != nil {
		s.AuditObserver(string(kind))
	}
}

func actorJSON(a audit.Actor) []byte {
	const tpl = `{"kind":%q,"id":%q}`
	return []byte(fmt.Sprintf(tpl, a.Kind, a.ID))
}

// PruneSecurityAudit deletes audit rows older than the retention horizon and
// returns how many rows went away. The reconciler calls this with
// CISYNC_CTRL_AUDIT_RETENTION_DAYS (default 90; B7 requires >=90d).
func (s *Store) PruneSecurityAudit(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM ctrl.security_audit WHERE ts < $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("prune security audit: %w", err)
	}
	return tag.RowsAffected(), nil
}
