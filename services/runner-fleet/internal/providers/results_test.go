package providers

import (
	"context"
	"testing"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// P0-2 / I-01: providers must report a REAL outcome census on every
// terminal result so control-plane evidence validation never has to infer
// pass/fail from job status alone.

func TestSimResultsDeterministicAndBiasConsistent(t *testing.T) {
	p := NewSim()
	for _, tc := range []struct {
		runID string
		bias  string
	}{
		{"run_res_pass_1", "pass"}, {"run_res_pass_2", "pass"},
		{"run_res_fail_a", "fail:deterministic_regression"},
		{"run_res_fail_b", "fail:functional_regression"},
		{"run_res_flaky", "flaky:0.9"},
	} {
		h, err := p.Submit(context.Background(), domain.Job{
			RunID: tc.runID, Attempt: 1, Pool: "sim",
			Spec: domain.JobSpec{
				Kind: "selected_unit", TimeoutMS: 60000,
				SimProfile: &domain.SimProfile{DurationMS: 1, OutcomeBias: tc.bias},
			},
		})
		if err != nil {
			t.Fatalf("submit %s: %v", tc.runID, err)
		}
		var state domain.PollState
		var oc domain.Outcome
		for i := 0; i < 100; i++ { // poll until the simulated duration elapses
			state, oc = p.Poll(h)
			if state == domain.PollDone {
				break
			}
			time.Sleep(500 * time.Microsecond)
		}
		if state != domain.PollDone {
			t.Fatalf("%s must finish under the simulated duration", tc.runID)
		}
		r := oc.Results
		if r == nil {
			t.Fatalf("%s: outcome must carry a results census", tc.runID)
		}
		if r.Total <= 0 || r.Sum() != r.Total {
			t.Fatalf("%s: inconsistent census %+v", tc.runID, *r)
		}
		switch {
		case oc.Status == domain.StatusSucceeded && r.Passed != r.Total:
			t.Fatalf("%s: success bias must be all-passed, got %+v", tc.runID, *r)
		case oc.Status == domain.StatusFailed && r.Failed == 0:
			t.Fatalf("%s: failure bias must include executed failures, got %+v", tc.runID, *r)
		}
	}
}

func TestSimResultsIdenticalForIdenticalInputs(t *testing.T) {
	p := NewSim()
	job := domain.Job{
		RunID: "run_det_res", Attempt: 3, Pool: "sim",
		Spec: domain.JobSpec{Kind: "selected_unit", TimeoutMS: 60000,
			SimProfile: &domain.SimProfile{DurationMS: 1, OutcomeBias: "pass"}},
	}
	var first *domain.TestResults
	for i := 0; i < 2; i++ {
		h, _ := p.Submit(context.Background(), job)
		_, oc := p.Poll(h)
		if first == nil {
			first = oc.Results
			continue
		}
		if *first != *oc.Results {
			t.Fatalf("identical inputs must reproduce identical census: %+v vs %+v", *first, *oc.Results)
		}
	}
}

func TestDockerOutcomeCensusFromExitCode(t *testing.T) {
	success := censusFromExit(nil)
	if success.Total != 1 || success.Passed != 1 || success.Failed != 0 {
		t.Fatalf("exit 0 must census one passed test: %+v", success)
	}
	failure := censusFromExit(errExitNonZero)
	if failure.Total != 1 || failure.Passed != 0 || failure.Failed != 1 {
		t.Fatalf("nonzero exit must census one failed test: %+v", failure)
	}
}

var errExitNonZero = &fakeExitError{}
