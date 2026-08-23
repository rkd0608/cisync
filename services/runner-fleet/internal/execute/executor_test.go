package execute

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	"sauron.dev/sauron/runner-fleet/internal/providers"
	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// failingProvider simulates an unavailable substrate (docker daemon down…).
type failingProvider struct{}

func (failingProvider) Submit(context.Context, domain.Job) (domain.Handle, error) {
	return nil, errors.New("provider unavailable: connection refused")
}
func (failingProvider) Cancel(domain.Handle) error { return nil }
func (failingProvider) Poll(domain.Handle) (domain.PollState, domain.Outcome) {
	return domain.PollDone, domain.Outcome{}
}

// scriptedProvider lets tests hold a job mid-flight and inject outcomes.
type scriptedProvider struct {
	mu       sync.Mutex
	started  chan string
	release  map[string]domain.Outcome
	cancels  []string
	handleOf map[string]*scriptedHandle
}

type scriptedHandle struct{ runID string }

func newScriptedProvider() *scriptedProvider {
	return &scriptedProvider{
		started:  make(chan string, 16),
		release:  make(map[string]domain.Outcome),
		handleOf: make(map[string]*scriptedHandle),
	}
}

func (p *scriptedProvider) Submit(_ context.Context, job domain.Job) (domain.Handle, error) {
	p.mu.Lock()
	h := &scriptedHandle{runID: job.RunID}
	p.handleOf[job.RunID] = h
	p.mu.Unlock()
	p.started <- job.RunID
	return h, nil
}

func (p *scriptedProvider) Cancel(handle domain.Handle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancels = append(p.cancels, handle.(*scriptedHandle).runID)
	return nil
}

func (p *scriptedProvider) Poll(handle domain.Handle) (domain.PollState, domain.Outcome) {
	h := handle.(*scriptedHandle)
	p.mu.Lock()
	outcome, ok := p.release[h.runID]
	p.mu.Unlock()
	if !ok {
		return domain.PollRunning, domain.Outcome{}
	}
	if outcome.Logs == nil {
		outcome.Logs = []byte("scripted logs\n")
	}
	outcome.Artifacts = []domain.Artifact{{
		Name:      "report.json",
		Digest:    providers.DigestOf(outcome.Logs),
		SizeBytes: int64(len(outcome.Logs)),
	}}
	return domain.PollDone, outcome
}

func (p *scriptedProvider) releaseWith(runID string, o domain.Outcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.release[runID] = o
}

func newTestStore(t *testing.T) *fstore.MemoryStore {
	t.Helper()
	return fstore.NewMemoryStore(time.Now)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestProviderUnavailableMarksFailedInfraTransient(t *testing.T) {
	st := newTestStore(t)
	job := domain.Job{
		RunID: "run-infra", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "selected_unit", TimeoutMS: 60000},
	}
	if err := st.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, failingProvider{}, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, time.Hour, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go exec.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		j, err := st.Get(context.Background(), "run-infra")
		return err == nil && j.Accepted && j.Status == domain.StatusFailed
	})
	cancel()

	j, _ := st.Get(context.Background(), "run-infra")
	if !j.Accepted || j.Status != domain.StatusFailed {
		t.Fatalf("expected accepted failure, got status=%s accepted=%v", j.Status, j.Accepted)
	}
	if class, _ := j.ResultRef["class"].(string); class != "infra_transient" {
		t.Fatalf("result_ref.class must be infra_transient, got %v", j.ResultRef["class"])
	}
	digest, _ := j.ResultRef["logs_digest"].(string)
	if len(digest) != len("sha256:")+64 {
		t.Fatalf("logs_digest must be well formed, got %q", digest)
	}
}

func TestExecutorRunsJobsToEndToEndAccepted(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"run-e2e-1", "run-e2e-2", "run-e2e-3"} {
		err := st.Enqueue(context.Background(), domain.Job{
			RunID: id, Attempt: 1, Tier: 1, Pool: "sim",
			Spec: domain.JobSpec{
				Kind: "selected_unit", TimeoutMS: 60000,
				SimProfile: &domain.SimProfile{DurationMS: 20, OutcomeBias: "pass"},
			},
		})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	exec := New(st, providers.NewSim(), NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 2, 100*time.Millisecond, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go exec.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		depth, err := st.QueueDepth(context.Background())
		if err != nil {
			return false
		}
		total := int64(0)
		for _, n := range depth {
			total += n
		}
		return total == 0
	})
	waitFor(t, 3*time.Second, func() bool {
		for _, id := range []string{"run-e2e-1", "run-e2e-2", "run-e2e-3"} {
			j, err := st.Get(context.Background(), id)
			if err != nil || !j.Accepted || j.Status != domain.StatusSucceeded {
				return false
			}
			ref := j.ResultRef
			if ref == nil || ref["logs_digest"] == "" {
				return false
			}
		}
		return true
	})
}

func TestExecutorDiscardsResultAfterReclaimI11(t *testing.T) {
	st := newTestStore(t)
	scripted := newScriptedProvider()
	err := st.Enqueue(context.Background(), domain.Job{
		RunID: "run-stolen", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "selected_unit", TimeoutMS: 600000},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, scripted, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, time.Hour, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go exec.Run(ctx)

	select {
	case <-scripted.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("executor never submitted the job")
	}

	requeued, err := st.RequeueStale(context.Background(), -time.Second, time.Now())
	if err != nil || len(requeued) != 1 {
		t.Fatalf("forced reclaim failed: %v %v", requeued, err)
	}

	scripted.releaseWith("run-stolen", domain.Outcome{Status: domain.StatusSucceeded, DurationMS: 10})

	waitFor(t, 2*time.Second, func() bool {
		j, err := st.Get(context.Background(), "run-stolen")
		return err == nil && j.FenceToken == 2
	})

	j, _ := st.Get(context.Background(), "run-stolen")
	if j.Accepted {
		t.Fatalf("stale holder must never mutate state (I-11): %+v", j)
	}
	if j.Status != domain.StatusQueued {
		t.Fatalf("job must remain queued for another worker, got %s", j.Status)
	}
	if j.ResultRef != nil {
		t.Fatalf("no result may be recorded from a stale fence: %v", j.ResultRef)
	}
}

func TestHeartbeatKeepsJobFreshDuringExecution(t *testing.T) {
	st := newTestStore(t)
	scripted := newScriptedProvider()
	err := st.Enqueue(context.Background(), domain.Job{
		RunID: "run-hb-live", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "api_compat", TimeoutMS: 600000},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, scripted, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, 10*time.Millisecond, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go exec.Run(ctx)

	select {
	case <-scripted.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("executor never submitted the job")
	}

	var firstHB time.Time
	waitFor(t, 2*time.Second, func() bool {
		j, err := st.Get(context.Background(), "run-hb-live")
		if err != nil || j.LastHeartbeat.IsZero() {
			return false
		}
		firstHB = j.LastHeartbeat
		return true
	})
	time.Sleep(80 * time.Millisecond)
	j, _ := st.Get(context.Background(), "run-hb-live")
	if !j.LastHeartbeat.After(firstHB) {
		t.Fatalf("heartbeat goroutine must refresh last_heartbeat (%v -> %v)", firstHB, j.LastHeartbeat)
	}
	scripted.releaseWith("run-hb-live", domain.Outcome{Status: domain.StatusSucceeded})
	waitFor(t, 2*time.Second, func() bool {
		done, err := st.Get(context.Background(), "run-hb-live")
		return err == nil && done.Accepted
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
