// Package execute runs the embedded worker pool: claim → Submit → Poll →
// complete, heartbeating each running job (I-11 fencing applies end to end).
package execute

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/obs"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// Executor claims and executes jobs from one pool via one provider.
type Executor struct {
	store             store.Store
	provider          domain.Provider
	registry          *Registry
	metrics           *obs.Metrics
	logger            *slog.Logger
	pool              string
	providerName      string
	workerID          string
	concurrency       int
	heartbeatInterval time.Duration
	pollInterval      time.Duration
	nowFn             func() time.Time
	// LeaseVerifier gates internal mutations behind the job-lease credential
	// (B2/I-04): a completion from a worker that cannot prove its dispatch-
	// time lease is discarded exactly like a stale-fence result. nil fails
	// closed.
	LeaseVerifier *joblease.Verifier
}

// New builds an executor for a pool. nowFn is injectable for tests.
func New(st store.Store, p domain.Provider, reg *Registry, m *obs.Metrics, logger *slog.Logger, pool, providerName string, concurrency int, heartbeatInterval, pollInterval time.Duration, leaseVerifier *joblease.Verifier) *Executor {
	return &Executor{
		store:             st,
		provider:          p,
		registry:          reg,
		metrics:           m,
		logger:            logger,
		pool:              pool,
		providerName:      providerName,
		workerID:          fmt.Sprintf("worker_%s_%d", pool, time.Now().UnixNano()),
		concurrency:       concurrency,
		heartbeatInterval: heartbeatInterval,
		pollInterval:      pollInterval,
		nowFn:             time.Now,
		LeaseVerifier:     leaseVerifier,
	}
}

// Run blocks claiming and executing jobs until ctx is cancelled.
func (e *Executor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < e.concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			// Per-slot identity: a shared worker id would serialize claims
			// and heartbeats on one fleet.workers row (W3 storm finding).
			slotID := fmt.Sprintf("%s_slot%d", e.workerID, slot)
			if err := e.store.EnsureWorker(ctx, slotID, e.pool, 1, e.nowFn()); err != nil {
				e.logger.Error("worker registration failed", slog.String("err", err.Error()))
				return
			}
			e.loop(ctx, slotID)
		}(i)
	}
	wg.Wait()
}

func (e *Executor) loop(ctx context.Context, workerID string) {
	for {
		if ctx.Err() != nil {
			return
		}
		jobs, err := e.store.ClaimJobs(ctx, store.Claim{
			Pool:     e.pool,
			Provider: e.providerName,
			Limit:    1,
			WorkerID: workerID,
		}, e.nowFn())
		if err != nil {
			e.logger.Error("claim failed", slog.String("err", err.Error()))
			sleepCtx(ctx, e.pollInterval)
			continue
		}
		if len(jobs) == 0 {
			sleepCtx(ctx, e.pollInterval)
			continue
		}
		e.metrics.CounterInc("fleet_claims_total", "Jobs claimed by workers", "pool", e.pool)
		e.runJob(ctx, jobs[0])
	}
}

// runJob drives one claimed job through Submit/Poll/complete with a per-job
// heartbeat goroutine and hard timeout.
func (e *Executor) runJob(ctx context.Context, job domain.Job) {
	startedAt := e.nowFn()

	handle, err := e.provider.Submit(ctx, job)
	if err != nil {
		e.logger.Warn("provider submit failed", slog.String("run_id", job.RunID), slog.String("err", err.Error()))
		e.completeInternal(ctx, job, domain.Outcome{
			Status:         domain.StatusFailed,
			Classification: "infra_transient",
			Logs:           []byte(fmt.Sprintf("provider unavailable: %v\n", err)),
			DurationMS:     0,
		})
		return
	}
	e.registry.Register(job.RunID, handle)
	defer e.registry.Unregister(job.RunID)

	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(e.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatDone:
				return
			case <-ticker.C:
				// Heartbeats ride the job-lease gate too: an unprovable
				// credential stops the loop (B2/I-04, same as completion).
				if !e.heartbeatOnce(job) {
					return
				}
			}
		}
	}()

	timeout := 15 * time.Minute
	if job.Spec.TimeoutMS > 0 {
		timeout = time.Duration(job.Spec.TimeoutMS) * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var outcome domain.Outcome
	timedOut := false
loop:
	for {
		select {
		case <-ctx.Done():
			_ = e.provider.Cancel(handle)
			return
		case <-deadline.C:
			timedOut = true
			_ = e.provider.Cancel(handle)
			state, oc := e.provider.Poll(handle)
			if state != domain.PollDone || oc.Status == "" {
				outcome = domain.Outcome{
					Status:         domain.StatusTimedOut,
					Classification: "timeout",
					Logs:           []byte("job exceeded timeout_ms; killed\n"),
				}
			} else {
				outcome = oc
				outcome.Status = domain.StatusTimedOut
			}
			break loop
		default:
			state, oc := e.provider.Poll(handle)
			if state == domain.PollDone && oc.Status != "" {
				outcome = oc
				break loop
			}
			sleepCtx(ctx, e.pollInterval)
			if ctx.Err() != nil {
				return
			}
		}
	}
	close(heartbeatDone)

	if timedOut && outcome.Classification == "" {
		outcome.Classification = "timeout"
	}
	if outcome.DurationMS == 0 {
		outcome.DurationMS = e.nowFn().Sub(startedAt).Milliseconds()
	}
	e.completeInternal(ctx, job, outcome)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
