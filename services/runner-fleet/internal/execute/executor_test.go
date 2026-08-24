package execute

import (
	"context"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/obs"
	"sauron.dev/sauron/runner-fleet/internal/providers"
)

func TestProviderUnavailableMarksFailedInfraTransient(t *testing.T) {
	st := newTestStore(t)
	lease := newTestLease(t)
	job := lease.mintFor(domain.Job{
		RunID: "run-infra", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "selected_unit", TimeoutMS: 60000},
	})
	if err := st.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, failingProvider{}, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, time.Hour, 10*time.Millisecond, lease.verifier)
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
	lease := newTestLease(t)
	for _, id := range []string{"run-e2e-1", "run-e2e-2", "run-e2e-3"} {
		job := lease.mintFor(domain.Job{
			RunID: id, Attempt: 1, Tier: 1, Pool: "sim",
			Spec: domain.JobSpec{
				Kind: "selected_unit", TimeoutMS: 60000,
				SimProfile: &domain.SimProfile{DurationMS: 20, OutcomeBias: "pass"},
			},
		})
		if err := st.Enqueue(context.Background(), job); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	exec := New(st, providers.NewSim(), NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 2, 100*time.Millisecond, 5*time.Millisecond, lease.verifier)
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
	lease := newTestLease(t)
	scripted := newScriptedProvider()
	err := st.Enqueue(context.Background(), lease.mintFor(domain.Job{
		RunID: "run-stolen", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "selected_unit", TimeoutMS: 600000},
	}))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, scripted, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, time.Hour, 5*time.Millisecond, lease.verifier)
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
	lease := newTestLease(t)
	scripted := newScriptedProvider()
	err := st.Enqueue(context.Background(), lease.mintFor(domain.Job{
		RunID: "run-hb-live", Attempt: 1, Tier: 1, Pool: "sim",
		Spec: domain.JobSpec{Kind: "api_compat", TimeoutMS: 600000},
	}))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	exec := New(st, scripted, NewRegistry(), obs.New(), quietLogger(),
		"sim", "sim", 1, 10*time.Millisecond, 5*time.Millisecond, lease.verifier)
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
