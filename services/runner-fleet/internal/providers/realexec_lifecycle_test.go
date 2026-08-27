package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// realexec_lifecycle_test.go covers honest-degradation and timeout paths of
// the realexec provider; envelope mapping lives in realexec_provider_test.go.
func TestRealExecMissingBundleSkipsHonestly(t *testing.T) {
	dir := t.TempDir()
	reposDir := newReposDir(t)
	fakeMode(t, "")
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	job := newRealexecJob("")
	outcome := pollUntil(t, p, mustSubmit(t, p, job))
	if outcome.Status != domain.StatusSucceeded || outcome.Classification != "bundle_unavailable" {
		t.Fatalf("missing bundle must skip honestly: %+v %+v", outcome.Status, outcome.Classification)
	}
	if outcome.Results.Skipped != 1 || outcome.Results.Passed != 0 {
		t.Fatalf("census must record exactly one skip: %+v", *outcome.Results)
	}
	if _, err := os.Stat(filepath.Join(dir, "argv.log")); err == nil {
		t.Fatalf("no container may run without a bundle")
	}
}

// A repo with no recognizable stack cannot run any check — same honesty rule:
// success status with all-skipped census plus an explicit detection reason.
func TestRealExecNoPresetSkips(t *testing.T) {
	dir := t.TempDir()
	reposDir := newReposDir(t)
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{"README.md": "hi"})
	fakeMode(t, "pass")
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	outcome := pollUntil(t, p, mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz"))))
	if outcome.Status != domain.StatusSucceeded || outcome.Results.Skipped != 1 {
		t.Fatalf("no-preset repo must skip with reason: %+v %+v", outcome.Status, outcome.Results)
	}
	checks := decodeChecksArtifact(t, outcome.Artifacts)
	if !strings.Contains(checks[0].Detail, "preset") && checks[0].Tool != "preset_detect" {
		t.Fatalf("detection reason must surface: %+v", checks)
	}
}

// Timeout enforcement under a FAKE clock: no wall-clock waiting; advancing
// the injected clock past the deadline kills the hanging fake container and
// yields timed_out with whatever was executed so far attached.
func TestRealExecTimeoutEnforcementFakeClock(t *testing.T) {
	dir := t.TempDir()
	reposDir := newReposDir(t)
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{"package.json": "{}"})
	fakeMode(t, "hang")
	now := time.Now()
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	p.nowFn = func() time.Time { return now }
	h := mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz")))
	now = now.Add(10 * time.Minute) // jump past the 30s job deadline
	state, _ := p.Poll(h)
	if state != domain.PollRunning {
		t.Fatalf("deadline jump must trigger kill asynchronously first, got %+v", state)
	}
	outcome := pollUntil(t, p, h)
	if outcome.Status != domain.StatusTimedOut || outcome.Classification != "timeout" {
		t.Fatalf("hang must end timed_out: %+v %+v", outcome.Status, outcome.Classification)
	}
	if outcome.Logs == nil {
		t.Fatalf("timed-out outcome must still carry logs")
	}
}

func TestRealExecMissingBinaryDegradesGracefully(t *testing.T) {
	reposDir := newReposDir(t)
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{"package.json": "{}"})
	p := NewRealExec("/nonexistent/docker-bin", "tools:test", "", reposDir)
	outcome := pollUntil(t, p, mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz"))))
	if outcome.Status != domain.StatusFailed || outcome.Classification != "exit_nonzero" {
		t.Fatalf("missing binary must degrade to failed/exit_nonzero: %+v", outcome)
	}
	if len(outcome.Logs) == 0 {
		t.Fatalf("failure logs must explain the substrate problem")
	}
}

func TestRealExecForeignHandleRejected(t *testing.T) {
	p := NewRealExec("docker", "tools:test", "", newReposDir(t))
	if err := p.Cancel("not-a-handle"); err == nil {
		t.Fatalf("foreign handle must error")
	}
	if _, oc := p.Poll("not-a-handle"); oc.Status != domain.StatusFailed {
		t.Fatalf("foreign handle poll must fail closed")
	}
}

// --- helpers ------------------------------------------------------------
