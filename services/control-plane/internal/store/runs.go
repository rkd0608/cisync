package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// QueuedRun is a scheduler scan row. Tier/cost/duration feed the engine
// admission pass; CreatedSeq is the logical clock (I-13).
type QueuedRun struct {
	ID                string
	TenantID          string
	CandidateID       string
	Pool              string
	Tier              int
	EstDurationMS     int64
	EstCostMillicents int64
	Priority          float64
	CreatedSeq        int64
	CreatedAt         time.Time
}

// QueuedRuns returns queued runs in effective-priority order with a
// deterministic tie-break priority DESC → age → id (I-13). Runs of candidates
// parked as blocked_representative stay unqueued until elected or superseded.
func (s *Store) QueuedRuns(ctx context.Context, limit int) ([]QueuedRun, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT r.id, r.tenant_id, r.candidate_id, r.pool, r.tier,
		        r.est_duration_ms, r.est_cost_millicents, r.priority, r.seq, r.created_at
		 FROM ctrl.validation_runs r
		 JOIN ctrl.candidates c ON c.id = r.candidate_id AND c.tenant_id = r.tenant_id
		 WHERE r.state='queued' AND c.state NOT IN ('blocked_representative','superseded','cancelled','rejected','eligible')
		 ORDER BY r.priority DESC, r.created_at ASC, r.id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("queued runs: %w", err)
	}
	defer rows.Close()
	var out []QueuedRun
	for rows.Next() {
		var r QueuedRun
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CandidateID, &r.Pool, &r.Tier,
			&r.EstDurationMS, &r.EstCostMillicents, &r.Priority, &r.CreatedSeq, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan queued run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InFlightByTier counts dispatched/running runs per tier — the WIP snapshot
// the admission pass draws against (I-10).
func (s *Store) InFlightByTier(ctx context.Context) (map[int]int, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT tier, count(*) FROM ctrl.validation_runs
		 WHERE state IN ('dispatched','running') GROUP BY tier`)
	if err != nil {
		return nil, fmt.Errorf("in flight by tier: %w", err)
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var tier int
		var n int
		if err := rows.Scan(&tier, &n); err != nil {
			return nil, fmt.Errorf("scan in-flight row: %w", err)
		}
		out[tier] = n
	}
	return out, rows.Err()
}

// GetRunByID loads one run regardless of tenant for internal scheduler paths;
// the tenant is read from the row itself, never from payloads.
func (s *Store) GetRunByID(ctx context.Context, runID string) (*domain.ValidationRun, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id, tenant_id, plan_id, candidate_id, tier, job_spec, attempt, pool,
		        est_duration_ms, est_cost_millicents, priority, fence_token, timeout_ms,
		        dispatched_at, finished_at, state, created_at
		 FROM ctrl.validation_runs WHERE id=$1`, runID)
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
		return nil, fmt.Errorf("get run by id: %w", err)
	}
	if err := json.Unmarshal(specRaw, &r.JobSpec); err != nil {
		return nil, fmt.Errorf("unmarshal job spec: %w", err)
	}
	r.State = domain.RunState(state)
	r.DispatchedAt = dispatchedAt
	r.FinishedAt = finishedAt
	return &r, nil
}

// CancelRunsForCandidateTx cancels all queued/dispatched runs of a candidate
// (supersede propagation) inside the effect tx, appending one
// validation.cancelled event per run. Dispatched runs release their I-06
// budget reservation by estimate in the SAME tx (conservation); queued runs
// reserved nothing.
func CancelRunsForCandidateTx(ctx context.Context, tx pgx.Tx, st *Store, tenantID, candidateID, reason string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, state, est_duration_ms FROM ctrl.validation_runs
		 WHERE tenant_id=$1 AND candidate_id=$2 AND state IN ('queued','dispatched')`,
		tenantID, candidateID)
	if err != nil {
		return nil, fmt.Errorf("runs for candidate: %w", err)
	}
	type cancelTarget struct {
		id       string
		state    string
		estDurMS int64
	}
	var targets []cancelTarget
	for rows.Next() {
		var t cancelTarget
		if err := rows.Scan(&t.id, &t.state, &t.estDurMS); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan run id: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	actor := domain.EventActor{Kind: string(domain.ActorSystem), ID: "scheduler"}
	var cancelled []string
	for _, target := range targets {
		ev, err := domain.NewEvent(tenantID,
			domain.AggregateRef{Type: string(domain.AggRun), ID: target.id},
			"validation.cancelled", "", domain.NewCorrelationID(), actor,
			map[string]any{"run_ids": toAnySlice([]string{target.id}), "reason": reason})
		if err != nil {
			return cancelled, err
		}
		if err := st.AppendEventsTx(ctx, tx, []*domain.Event{ev}); err != nil {
			return cancelled, err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ctrl.validation_runs SET state='cancelled', finished_at=now(), seq=$3
			 WHERE id=$1 AND tenant_id=$2 AND state IN ('queued','dispatched')`,
			target.id, tenantID, ev.Seq)
		if err != nil {
			return cancelled, fmt.Errorf("cancel run %s: %w", target.id, err)
		}
		if tag.RowsAffected() == 1 {
			cancelled = append(cancelled, target.id)
			if target.state == "dispatched" {
				if err := ReleaseBudgetsTx(ctx, tx, tenantID, ev.Seq, BudgetDeltas{
					BudgetCPUMinutes:           ActualCPUMinutes(0, target.estDurMS),
					BudgetConcurrentCandidates: 1,
				}); err != nil {
					return cancelled, err
				}
			}
		}
	}
	return cancelled, nil
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
			"run_id":       run.ID,
			"attempt":      run.Attempt,
			"fence_token":  run.FenceToken,
			"worker_id":    "pending-claim",
			"provider":     "sim",
			"candidate_id": run.CandidateID,
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
