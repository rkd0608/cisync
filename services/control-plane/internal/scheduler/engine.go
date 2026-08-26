package scheduler

import (
	"context"
	"sync"
	"time"

	"cisync.dev/cisync/control-plane/internal/joblease"
	policypkg "cisync.dev/cisync/control-plane/internal/policy"
	"cisync.dev/cisync/control-plane/internal/relay"
	"cisync.dev/cisync/control-plane/internal/store"
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
	store       *store.Store
	fleet       FleetGateway
	pool        string
	batch       int
	policy      PolicySource
	maxRetry    int
	leaseSigner *joblease.Signer

	// auditMu/auditNotify/deniedAudits back the B7 security-audit hooks
	// (see audit.go): notify fans out metric bumps, deniedAudits dedupes
	// per-run budget-denial emissions across ticks.
	auditMu      sync.Mutex
	auditNotify  func(kind string)
	deniedMu     sync.Mutex
	deniedAudits map[string]struct{}
}

// PolicySource supplies the resolved active policy: WIP caps by tier AND
// the budget ceilings the I-06 reservation accounting enforces.
type PolicySource func() policypkg.ResolvedPolicy

// DefaultPolicySource serves the compiled-in default pack resolved.
func DefaultPolicySource() policypkg.ResolvedPolicy {
	rec := policypkg.DefaultPolicyPack()
	return policypkg.ResolvedPolicy{PolicyID: rec.ID, Version: rec.Version, Body: rec.Body}
}

// NewEngine wires the engine scheduler. batch bounds per-tick work.
// leaseSigner mints the per-run job-lease credentials required by the fleet's
// authenticated mutation endpoints (B2/I-04); nil fails closed at dispatch.
func NewEngine(st *store.Store, fleet FleetGateway, pool string, batch int, leaseSigner *joblease.Signer) *EngineScheduler {
	if batch <= 0 {
		batch = 8
	}
	return &EngineScheduler{
		store:        st,
		fleet:        fleet,
		pool:         pool,
		batch:        batch,
		policy:       DefaultPolicySource,
		maxRetry:     2,
		leaseSigner:  leaseSigner,
		deniedAudits: map[string]struct{}{},
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
