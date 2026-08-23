package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// QueuedRun is a scheduler scan row.
type QueuedRun struct {
	ID          string
	TenantID    string
	CandidateID string
	Pool        string
	Priority    float64
	CreatedAt   time.Time
}

// QueuedRuns returns queued runs in effective-priority order with a
// deterministic tie-break priority DESC → age → id (I-13).
func (s *Store) QueuedRuns(ctx context.Context, limit int) ([]QueuedRun, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, tenant_id, candidate_id, pool, priority, created_at
		 FROM ctrl.validation_runs WHERE state='queued'
		 ORDER BY priority DESC, created_at ASC, id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("queued runs: %w", err)
	}
	defer rows.Close()
	var out []QueuedRun
	for rows.Next() {
		var r QueuedRun
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CandidateID, &r.Pool, &r.Priority, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan queued run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun loads one run within tenant.
func (s *Store) GetRun(ctx context.Context, tenantID, runID string) (*domain.ValidationRun, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, tenant_id, plan_id, candidate_id, tier, job_spec, attempt, pool,
		        est_duration_ms, est_cost_millicents, priority, fence_token, timeout_ms,
		        dispatched_at, finished_at, state, created_at
		 FROM ctrl.validation_runs WHERE id=$1 AND tenant_id=$2`, runID, tenantID)
	var r domain.ValidationRun
	var specRaw []byte
	var state string
	var dispatchedAt, finishedAt *time.Time
	err := row.Scan(&r.ID, &r.TenantID, &r.PlanID, &r.CandidateID, &r.Tier, &specRaw,
		&r.Attempt, &r.Pool, &r.EstDurationMS, &r.EstCostMillicents, &r.Priority,
		&r.FenceToken, &r.TimeoutMS, &dispatchedAt, &finishedAt, &state, &r.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get run: %w", err)
	}
	if err := json.Unmarshal(specRaw, &r.JobSpec); err != nil {
		return nil, fmt.Errorf("unmarshal job spec: %w", err)
	}
	r.State = domain.RunState(state)
	r.DispatchedAt = dispatchedAt
	r.FinishedAt = finishedAt
	return &r, nil
}

// DispatchRunTx transitions a queued run to dispatched with a fresh fence
// token and appends validation.started; it is conditional on the run still
// being queued so double-dispatch is impossible.
func DispatchRunTx(ctx context.Context, tx pgx.Tx, s *Store, run *domain.ValidationRun) (*domain.Event, error) {
	run.Apply("run.dispatched")
	corr := domain.NewCorrelationID()
	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	ev, err := domain.NewEvent(run.TenantID,
		domain.AggregateRef{Type: string(domain.AggRun), ID: run.ID},
		"validation.started", "", corr, actor, map[string]any{
			"run_id":      run.ID,
			"attempt":     run.Attempt,
			"fence_token": run.FenceToken,
			"worker_id":   "pending-claim",
			"provider":    "sim",
		})
	if err != nil {
		return nil, err
	}
	if err := s.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.validation_runs SET state='dispatched', fence_token=$3, seq=$4, dispatched_at=now()
		 WHERE id=$1 AND tenant_id=$2 AND state='queued'`,
		run.ID, run.TenantID, run.FenceToken, ev.Seq)
	if err != nil {
		return nil, fmt.Errorf("dispatch run update: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, fmt.Errorf("%w: run %s no longer queued", domain.ErrConflict, run.ID)
	}
	return ev, nil
}

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
