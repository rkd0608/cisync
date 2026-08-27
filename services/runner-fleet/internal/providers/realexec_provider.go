package providers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// NOT-FOR-PRODUCTION (THREAT_MODEL B5 / ARCHITECTURE §5): RealExecProvider
// executes control-plane-materialized repo snapshots inside docker sandboxes
// carrying the same defense-in-depth flags as DockerProvider (--network none,
// read-only rootfs, tmpfs scratch, memory/cpu/pid caps, non-root uid,
// cap-drop ALL). Opt-in via CISYNC_FLEET_PROVIDER=realexec.
//
// TOKEN FLOW (B5 hard rule: runners never hold credentials): runners NEVER
// fetch or embed tokens. The control-plane materializer downloads the head-
// state archive with its OWN installation token into the shared cisync-repos
// volume; this provider copies local bytes only, and the sandbox itself has
// no egress at all. Nothing secret crosses into the executed workload.
//
// HONESTY CONTRACT: a check either really executes (genuine pass/fail) or is
// reported as an explicit skip-with-reason; no code path synthesizes passes.
// Missing bundles, unrecognized stacks, absent toolchains and unusable
// archives degrade to all-skipped censuses (skipped ≠ positive evidence per
// I-01), keeping the completion/evidence contract untouched.

// DefaultRealExecTimeout applies when job_spec.timeout_ms is unset (v0 brief).
const DefaultRealExecTimeout = 10 * time.Minute

// MaxRealExecTimeout hard-caps any spec override so a hostile job_spec cannot
// wedge a worker slot indefinitely.
const MaxRealExecTimeout = 30 * time.Minute

// RealExecProvider executes real stack presets against pre-fetched bundles.
type RealExecProvider struct {
	Bin        string // docker CLI binary path/name
	ToolsImage string // tools image (node + eslint + tsc + python3)
	GoImage    string // optional golang image; empty ⇒ go preset skips with reason
	ReposDir   string // shared cisync-repos volume mount point
	nowFn      func() time.Time

	handles sync.Map
}

// NewRealExec wires the provider; the injected clock keeps timeout behavior
// deterministically testable.
func NewRealExec(bin, toolsImage, goImage, reposDir string) *RealExecProvider {
	return &RealExecProvider{Bin: bin, ToolsImage: toolsImage, GoImage: goImage, ReposDir: reposDir, nowFn: time.Now}
}

// RealExecHandle tracks one in-flight execution plus its hard deadline so
// polls can enforce cancellation against any clock.
type RealExecHandle struct {
	mu            sync.Mutex
	done          bool
	outcome       domain.Outcome
	cancel        context.CancelFunc
	deadline      time.Time
	killedByClock bool
}

// Submit never fails synchronously: every degradation lands as a terminal
// outcome with honest census instead of provider-unavailable churn.
func (p *RealExecProvider) Submit(ctx context.Context, job domain.Job) (domain.Handle, error) {
	runCtx, cancel := context.WithCancel(ctx)
	timeout := p.timeoutFor(job)
	startedAt := p.nowFn()
	h := &RealExecHandle{cancel: cancel, deadline: startedAt.Add(timeout)}
	p.handles.Store(h, struct{}{})
	go func() {
		defer p.handles.Delete(h)
		defer func() {
			if r := recover(); r != nil { // a panic must strand neither slot nor result
				p.terminate(h, p.infraFailure(job, startedAt, fmt.Errorf("realexec panic: %v", r)))
			}
		}()
		p.terminate(h, p.execute(runCtx, job, startedAt, h.deadline))
	}()
	return h, nil
}

func (p *RealExecProvider) timeoutFor(job domain.Job) time.Duration {
	if ms := job.Spec.TimeoutMS; ms > 0 {
		d := time.Duration(ms) * time.Millisecond
		if d > MaxRealExecTimeout {
			return MaxRealExecTimeout
		}
		return d
	}
	return DefaultRealExecTimeout
}

func (p *RealExecProvider) terminate(h *RealExecHandle, oc domain.Outcome) {
	h.mu.Lock()
	if !h.done {
		h.outcome = oc
		h.done = true
	}
	h.mu.Unlock()
}

// Cancel best-effort kills the child container; repeats are harmless.
func (p *RealExecProvider) Cancel(handle domain.Handle) error {
	h, ok := handle.(*RealExecHandle)
	if !ok {
		return fmt.Errorf("realexec provider: foreign handle %T", handle)
	}
	h.cancel()
	return nil
}

// Poll reports Running until terminal. The hard deadline fires HERE with the
// injected clock (testable without sleeping): past-deadline polls kill the
// child exactly once; the worker goroutine then records timed_out itself.
func (p *RealExecProvider) Poll(handle domain.Handle) (domain.PollState, domain.Outcome) {
	h, ok := handle.(*RealExecHandle)
	if !ok {
		return domain.PollDone, domain.Outcome{Status: domain.StatusFailed, Classification: "infra_transient"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.done && !h.killedByClock && p.nowFn().After(h.deadline) {
		h.killedByClock = true
		h.cancel()
	}
	if h.done {
		return domain.PollDone, h.outcome
	}
	return domain.PollRunning, domain.Outcome{}
}
