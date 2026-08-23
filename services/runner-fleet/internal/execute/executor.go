// Package execute runs the embedded worker pool: claim → Submit → Poll →
// complete, heartbeating each running job (I-11 fencing applies end to end).
package execute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	"sauron.dev/sauron/runner-fleet/internal/providers"
	"sauron.dev/sauron/runner-fleet/internal/store"
)

// Registry exposes active provider handles so the cancel endpoint can reach
// the substrate best-effort.
type Registry struct {
	mu      sync.Mutex
	handles map[string]domain.Handle
}

// NewRegistry returns an empty handle registry.
func NewRegistry() *Registry {
	return &Registry{handles: make(map[string]domain.Handle)}
}

// Register records the handle for a running run_id.
func (r *Registry) Register(runID string, h domain.Handle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handles[runID] = h
}

// Unregister drops the handle once the job leaves running state.
func (r *Registry) Unregister(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handles, runID)
}

// Lookup returns the active handle for a run_id, if any.
func (r *Registry) Lookup(runID string) (domain.Handle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.handles[runID]
	return h, ok
}

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
}

// New builds an executor for a pool. nowFn is injectable for tests.
func New(st store.Store, p domain.Provider, reg *Registry, m *obs.Metrics, logger *slog.Logger, pool, providerName string, concurrency int, heartbeatInterval, pollInterval time.Duration) *Executor {
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
	}
}

// Run blocks claiming and executing jobs until ctx is cancelled.
func (e *Executor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < e.concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			e.loop(ctx)
		}(i)
	}
	wg.Wait()
}

func (e *Executor) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		jobs, err := e.store.ClaimJobs(ctx, store.Claim{
			Pool:     e.pool,
			Provider: e.providerName,
			Limit:    1,
			WorkerID: e.workerID,
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
				hbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := e.store.Heartbeat(hbCtx, job.RunID, job.FenceToken, e.nowFn())
				cancel()
				if err != nil {
					e.logger.Warn("heartbeat rejected", slog.String("run_id", job.RunID), slog.String("err", err.Error()))
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

// completeInternal is the executor-side completion path; a fence rejection
// means the job was cancelled or reclaimed elsewhere and the result MUST be
// discarded without mutating state (I-11).
func (e *Executor) completeInternal(ctx context.Context, job domain.Job, outcome domain.Outcome) {
	digests := make([]string, 0, len(outcome.Artifacts))
	for _, a := range outcome.Artifacts {
		digests = append(digests, a.Digest)
	}
	logsDigest := providers.DigestOf(outcome.Logs)
	err := e.store.Complete(ctx, job.RunID, store.Completion{
		FenceToken:           job.FenceToken,
		Status:               outcome.Status,
		LogsDigest:           logsDigest,
		ArtifactDigests:      digests,
		DurationMS:           outcome.DurationMS,
		ActualCostMilliCents: outcome.CostMilliCents,
		Classification:       outcome.Classification,
	}, e.nowFn())
	switch {
	case err == nil:
		if recordErr := e.store.RecordArtifacts(ctx, job.RunID, outcome.Artifacts, e.nowFn()); recordErr != nil {
			e.logger.Warn("artifact recording failed", slog.String("run_id", job.RunID), slog.String("err", recordErr.Error()))
		}
		e.metrics.CounterInc("fleet_completions_total", "Accepted job completions", "status", outcome.Status)
		e.logger.Info("job completed",
			slog.String("run_id", job.RunID),
			slog.String("status", outcome.Status),
			slog.String("logs_digest", logsDigest))
	case errors.Is(err, domain.ErrFenceMismatch):
		e.metrics.CounterInc("fleet_completions_rejected_total", "Completions rejected by the fence gate", "reason", "fence_mismatch")
		e.logger.Warn("stale fence on internal completion; result discarded",
			slog.String("run_id", job.RunID), slog.String("err", err.Error()))
	default:
		e.metrics.CounterInc("fleet_completions_rejected_total", "Completions rejected by the fence gate", "reason", "already_accepted")
		e.logger.Warn("completion not accepted", slog.String("run_id", job.RunID), slog.String("err", err.Error()))
	}
}

// SweepStale requeues running jobs whose worker stopped heartbeating and
// purges dead workers; it runs once per tick until ctx ends.
func (e *Executor) SweepStale(ctx context.Context, threshold time.Duration, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requeued, err := e.store.RequeueStale(ctx, threshold, e.nowFn())
			if err != nil {
				e.logger.Error("stale sweep failed", slog.String("err", err.Error()))
				continue
			}
			for _, runID := range requeued {
				e.logger.Warn("requeued stale job", slog.String("run_id", runID))
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
