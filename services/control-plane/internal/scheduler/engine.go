package scheduler

import (
	"context"
	"time"

	policypkg "sauron.dev/sauron/control-plane/internal/policy"
	"sauron.dev/sauron/control-plane/internal/relay"
	"sauron.dev/sauron/control-plane/internal/store"
)

// FleetGateway is the runner-fleet surface the engine scheduler needs
// (internal-protocols §2): claim queued jobs and read accepted completions.
type FleetGateway interface {
	Enqueue(ctx context.Context, req relay.EnqueueRequest) error
	Completed(ctx context.Context, limit int) ([]relay.CompletedJob, error)
	Cancel(ctx context.Context, runID, reason string) error
}

// EngineScheduler implements domain.Scheduler with the real engines:
//
//	Tick = rank+admit queued runs (Priority/Admit pure functions) → dispatch
//	       via the fleet claim API → ingest accepted completions → evidence
//	       evaluation → failure classification/routing → decision rendering.
//
// WIP caps and budgets come from the active policy pack; admission denies
// overflow instead of overrunning it (I-10).
type EngineScheduler struct {
	store    *store.Store
	fleet    FleetGateway
	pool     string
	batch    int
	policy   PolicySource
	maxRetry int
}

// PolicySource supplies the policy-derived admission limits (WIP caps by
// tier) from the active pack.
type PolicySource func() (wipByTier map[int]int)

// DefaultPolicySource serves WIP caps from the compiled-in default pack.
func DefaultPolicySource() map[int]int {
	rec := policypkg.DefaultPolicyPack()
	wip := make(map[int]int, len(rec.Body.Budgets.WIPByTier))
	for tierText, capValue := range rec.Body.Budgets.WIPByTier {
		tier := 0
		for _, c := range tierText {
			if c < '0' || c > '9' {
				tier = -1
				break
			}
			tier = tier*10 + int(c-'0')
		}
		if tier < 0 || capValue < 0 {
			continue
		}
		wip[tier] = capValue
	}
	return wip
}

// NewEngine wires the engine scheduler. batch bounds per-tick work.
func NewEngine(st *store.Store, fleet FleetGateway, pool string, batch int) *EngineScheduler {
	if batch <= 0 {
		batch = 8
	}
	return &EngineScheduler{
		store:    st,
		fleet:    fleet,
		pool:     pool,
		batch:    batch,
		policy:   DefaultPolicySource,
		maxRetry: 2,
	}
}

// Tick implements domain.Scheduler.
func (e *EngineScheduler) Tick(ctx context.Context) (int, error) {
	dispatched, err := e.dispatchQueued(ctx)
	if err != nil {
		return 0, err
	}
	consumed, err := e.IngestCompletions(ctx)
	if err != nil {
		return dispatched, err
	}
	if consumed > 0 {
		logf("ingested %d completion(s)", consumed)
	}
	return dispatched, nil
}

// Run loops Tick until ctx is cancelled.
func (e *EngineScheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := e.Tick(ctx)
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
