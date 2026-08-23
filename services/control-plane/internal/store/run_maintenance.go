package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// CancelStaleDispatchedRuns cancels dispatched runs older than maxAge and
// appends validation.cancelled per run; returns cancelled ids.
func (s *Store) CancelStaleDispatchedRuns(ctx context.Context, maxAge time.Duration, reason string) ([]string, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	rows, err := s.Pool.Query(ctx,
		`SELECT id, tenant_id FROM ctrl.validation_runs WHERE state='dispatched' AND dispatched_at < $1 LIMIT 100`,
		cutoff)
	if err != nil {
		return nil, fmt.Errorf("stale runs scan: %w", err)
	}
	type stale struct {
		id, tenant string
	}
	var staleRuns []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.id, &st.tenant); err != nil {
			rows.Close()
			return nil, fmt.Errorf("stale runs row: %w", err)
		}
		staleRuns = append(staleRuns, st)
	}
	rows.Close()

	var cancelled []string
	for _, st := range staleRuns {
		err := s.withTx(ctx, func(tx pgx.Tx) error {
			actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "reconciler"}
			ev, err := domain.NewEvent(st.tenant,
				domain.AggregateRef{Type: string(domain.AggRun), ID: st.id},
				"validation.cancelled", "", domain.NewCorrelationID(), actor,
				map[string]any{"run_ids": []any{st.id}, "reason": reason})
			if err != nil {
				return err
			}
			if err := s.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx,
				`UPDATE ctrl.validation_runs SET state='cancelled', finished_at=now(), seq=$3
				 WHERE id=$1 AND tenant_id=$2 AND state IN ('queued','dispatched')`,
				st.id, st.tenant, ev.Seq)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				cancelled = append(cancelled, st.id)
			}
			return nil
		})
		if err != nil {
			return cancelled, err
		}
	}
	return cancelled, nil
}
