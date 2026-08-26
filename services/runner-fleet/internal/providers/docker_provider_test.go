package providers

import (
	"context"
	"testing"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// TestDockerProviderMissingBinaryFailsGracefully verifies the docker adapter
// degrades to failed/exit_nonzero when the substrate is absent — no daemon
// required. Full container behavior is covered by compose e2e suites.
func TestDockerProviderMissingBinaryFailsGracefully(t *testing.T) {
	p := NewDocker("/nonexistent/docker-bin", "busybox:1.36")
	h, err := p.Submit(context.Background(), domain.Job{
		RunID:   "run-docker-smoke",
		Attempt: 1,
		Pool:    "docker",
		Spec: domain.JobSpec{
			Kind:      "hermetic_build",
			Repo:      "acme/payments",
			TimeoutMS: 30000,
		},
	})
	if err != nil {
		t.Fatalf("submit must not fail synchronously: %v", err)
	}
	var outcome domain.Outcome
	for i := 0; i < 500; i++ {
		state, o := p.Poll(h)
		if state == domain.PollDone {
			outcome = o
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if outcome.Status != domain.StatusFailed {
		t.Fatalf("missing binary must surface failed, got %s", outcome.Status)
	}
	if len(outcome.Logs) == 0 {
		t.Fatalf("failure logs must be captured")
	}
	if d := DigestOf(outcome.Logs); len(d) != 71 {
		t.Fatalf("digest must be sha256:<64hex>, got %q", d)
	}
	if err := p.Cancel(h); err != nil {
		t.Fatalf("cancel after completion must stay best-effort clean, got %v", err)
	}
}

func TestDockerProviderForeignHandleRejected(t *testing.T) {
	p := NewDocker("/nonexistent/docker-bin", "busybox:1.36")
	if err := p.Cancel("not-a-handle"); err == nil {
		t.Fatalf("foreign handle must error")
	}
	state, outcome := p.Poll("not-a-handle")
	if state != domain.PollDone || outcome.Status != domain.StatusFailed {
		t.Fatalf("foreign handle must fail closed")
	}
}
