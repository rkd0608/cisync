package providers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
)

func simOutcome(t *testing.T, runID string, attempt int, spec domain.JobSpec) domain.Outcome {
	t.Helper()
	p := NewSim()
	h, err := p.Submit(context.Background(), domain.Job{RunID: runID, Attempt: attempt, Pool: "sim", Spec: spec})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for i := 0; i < 10000; i++ {
		state, outcome := p.Poll(h)
		if state == domain.PollDone {
			finalizeForTest(&outcome)
			return outcome
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sim never completed")
	return domain.Outcome{}
}

func finalizeForTest(o *domain.Outcome) {
	logs := SimLogs(o)
	o.Logs = logs
}

func TestSimDeterminismSameSeedSameResult(t *testing.T) {
	spec := domain.JobSpec{
		Kind: "selected_unit", Repo: "acme/payments", TimeoutMS: 60000,
		SimProfile: &domain.SimProfile{DurationMS: 5, OutcomeBias: "flaky:0.7"},
	}
	first := simOutcome(t, "run_det_01JAAAA", 1, spec)
	second := simOutcome(t, "run_det_01JAAAA", 1, spec)

	if first.Status != second.Status || first.Classification != second.Classification {
		t.Fatalf("outcomes diverge: %+v vs %+v", first, second)
	}
	if first.DurationMS != second.DurationMS {
		t.Fatalf("durations diverge: %d vs %d", first.DurationMS, second.DurationMS)
	}
	if string(first.Logs) != string(second.Logs) {
		t.Fatalf("logs diverge: %q vs %q", first.Logs, second.Logs)
	}
	if DigestOf(first.Logs) != DigestOf(second.Logs) {
		t.Fatalf("digests diverge")
	}

	different := simOutcome(t, "run_det_01JBBBB", 1, spec)
	_ = different
}

func TestSimDurationJitterWithinTwentyPercent(t *testing.T) {
	base := int64(4)
	spec := domain.JobSpec{Kind: "hermetic_build", TimeoutMS: 600000, SimProfile: &domain.SimProfile{DurationMS: base}}
	for i := 0; i < 100; i++ {
		runID := fmt.Sprintf("run-jitter-%04d", i)
		o := simOutcome(t, runID, 1, spec)
		if o.DurationMS < base*8/10 || o.DurationMS > base*12/10 {
			t.Fatalf("duration %d outside ±20%% of %d for %s", o.DurationMS, base, runID)
		}
	}
}

func TestSimOutcomeBiases(t *testing.T) {
	cases := []struct {
		bias       string
		wantStatus string
		wantClass  string
	}{
		{"pass", domain.StatusSucceeded, ""},
		{"fail:deterministic_regression", domain.StatusFailed, "deterministic_regression"},
		{"fail:infra_transient", domain.StatusFailed, "infra_transient"},
		{"flaky:0", domain.StatusFailed, "flake"},
		{"flaky:1", domain.StatusSucceeded, ""},
	}
	for _, tc := range cases {
		o := simOutcome(t, "run-bias-"+tc.bias, 1, domain.JobSpec{
			TimeoutMS:  60000,
			SimProfile: &domain.SimProfile{DurationMS: 2, OutcomeBias: tc.bias},
		})
		if o.Status != tc.wantStatus || o.Classification != tc.wantClass {
			t.Fatalf("bias %q → got (%s,%s), want (%s,%s)",
				tc.bias, o.Status, o.Classification, tc.wantStatus, tc.wantClass)
		}
	}
}

func TestSimCancelBeforeCompletionReportsCancelled(t *testing.T) {
	p := NewSim()
	spec := domain.JobSpec{TimeoutMS: 60000, SimProfile: &domain.SimProfile{DurationMS: 60000}}
	h, err := p.Submit(context.Background(), domain.Job{RunID: "run-cancel-sim", Attempt: 1, Spec: spec})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := p.Cancel(h); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	state, outcome := p.Poll(h)
	if state != domain.PollDone || outcome.Status != domain.StatusCancelled {
		t.Fatalf("cancelled sim must report cancelled, got state=%v status=%s", state, outcome.Status)
	}
}
