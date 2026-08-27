package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/redact"
)

// realexec_execute.go drives one job through sandbox prep → per-preset
// containers → contract-complete Outcome assembly.

// execute is the full lifecycle; cleanup ALWAYS removes the tmpdir so no
// agent-controlled tree survives its job (B5 hygiene).
func (p *RealExecProvider) execute(ctx context.Context, job domain.Job, startedAt, deadline time.Time) domain.Outcome {
	root, err := os.MkdirTemp("", "cisync-realexec-*")
	if err != nil {
		return p.infraFailure(job, startedAt, fmt.Errorf("sandbox tmpdir: %w", err))
	}
	defer os.RemoveAll(root)

	var logBuf bytes.Buffer
	// Job header first: guarantees structured logs for every terminal
	// outcome (even nothing-ran) and anchors forensic correlation.
	fmt.Fprintf(&logBuf, "[realexec] run=%s repo=%s head=%s provider=v0\n",
		job.RunID, job.Spec.Repo, job.Spec.HeadSHA)

	bundlePath, whyMissing := p.resolveBundle(job.Spec)
	if bundlePath == "" {
		checks := []CheckResult{SkippedOnlyCheck("materialize", "bundle_unavailable: "+whyMissing)}
		fmt.Fprintf(&logBuf, "bundle not staged; skipping checks\n%s\n", whyMissing)
		return p.finish(startedAt, deadline, domain.StatusSucceeded, "bundle_unavailable", checks, logBuf.Bytes())
	}

	workspace := filepath.Join(root, "workspace")
	scriptsDir := filepath.Join(root, "cisync")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return p.infraFailure(job, startedAt, err)
	}
	if err := writePresetScripts(scriptsDir); err != nil {
		return p.infraFailure(job, startedAt, err)
	}
	if _, err := extractTarball(bundlePath, workspace); err != nil {
		checks := []CheckResult{SkippedOnlyCheck("materialize", "bundle_unusable: "+err.Error())}
		fmt.Fprintf(&logBuf, "bundle extraction failed: %v\n", err)
		return p.finish(startedAt, deadline, domain.StatusSucceeded, "bundle_unusable", checks, logBuf.Bytes())
	}

	// GitHub archives are always prefixed with one top-level dir
	// (owner-repo-sha/); tool paths (package.json et al) live inside it.
	// Detection AND the sandbox mount both point at the effective root.
	execRoot := flattenSingleTopDir(workspace)

	presets := DetectPresets(execRoot)
	if len(presets) == 0 {
		checks := []CheckResult{SkippedOnlyCheck("preset_detect",
			"no_recognized_stack: none of package.json|*.py+manifest|go.mod present")}
		fmt.Fprintln(&logBuf, "no recognized stack detected; skipping all checks")
		return p.finish(startedAt, deadline, domain.StatusSucceeded, "no_recognized_stack", checks, logBuf.Bytes())
	}
	return p.runAllPresets(ctx, job, startedAt, deadline, execRoot, scriptsDir, presets, &logBuf)
}

// flattenSingleTopDir descends into the sole child when an archive ships a
// single wrapper directory; otherwise the workspace itself stays the root.
func flattenSingleTopDir(workspace string) string {
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return workspace
	}
	return filepath.Join(workspace, entries[0].Name())
}

// runAllPresets executes each preset container and aggregates honest checks.
func (p *RealExecProvider) runAllPresets(ctx context.Context, job domain.Job, startedAt, deadline time.Time,
	workspace, scriptsDir string, presets []Preset, logBuf *bytes.Buffer) domain.Outcome {

	var allChecks []CheckResult
	for _, preset := range presets {
		if ctx.Err() != nil || p.nowFn().After(deadline) {
			break // classifyRun below rules the run timed_out
		}
		image, ok := p.imageFor(preset)
		if !ok {
			allChecks = append(allChecks, SkippedOnlyCheck(preset.Name+"_preset",
				"toolchain_not_provisioned_v0: set CISYNC_FLEET_REALEXEC_GO_IMAGE to enable"))
			continue
		}
		var containerOut bytes.Buffer
		sink := io.MultiWriter(logBuf, &containerOut)
		exitCode, runErr := p.runContainer(ctx, image, preset.Name, workspace, scriptsDir, sink)
		checks := ParseCheckMarkers(containerOut.String())
		if runErr == nil && exitCode != 0 {
			checks = FallbackChecks(exitCode, checks)
		}
		if runErr != nil {
			// Substrate failure (binary missing, daemon gone): degrade exactly
			// like the docker provider instead of half-reporting evidence.
			return p.infraFailure(job, startedAt, fmt.Errorf("container %s: %w", preset.Name, runErr))
		}
		allChecks = append(allChecks, checks...)
	}
	status, classification := classifyRun(p.nowFn(), deadline, allChecks)
	return p.finish(startedAt, deadline, status, classification, allChecks, logBuf.Bytes())
}

func (p *RealExecProvider) imageFor(preset Preset) (string, bool) {
	switch preset.Name {
	case presetGo:
		return p.GoImage, p.GoImage != ""
	default:
		return p.ToolsImage, true
	}
}

// runContainer launches ONE locked-down container per preset streaming output
// into sink and returning its exit code. Flags mirror DockerProvider (B5);
// mounts stay read-only so agent code can never tamper with the bundle or the
// preset scripts, and all writable scratch lives on bounded tmpfs.
func (p *RealExecProvider) runContainer(ctx context.Context, image, presetName, workspace, scriptsDir string, sink io.Writer) (int, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{
		"run", "--rm",
		"--network", "none", // egress default-deny (§5 trust rule 3)
		"--read-only", // immutable rootfs
		"--memory", "512m",
		"--cpus", "1",
		"--pids-limit", "128",
		"--user", "65534:65534", // non-root inside the sandbox
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"-v", workspace + ":/src:ro",
		"-v", scriptsDir + ":/cisync:ro",
		// WHY uid/gid: docker tmpfs mounts are root-owned by default, which
		// made every write from the 65534 sandbox user fail with EACCES
		// (caught live during the tools-image smoke test).
		"--tmpfs", "/scratch:rw,size=192m,noexec,nosuid,uid=65534,gid=65534",
		"--tmpfs", "/tmp:rw,size=64m,noexec,nosuid,uid=65534,gid=65534",
		image,
		"/bin/sh", "-c", fmt.Sprintf("sh /cisync/%s.sh", presetName),
	}
	cmd := exec.CommandContext(runCtx, p.Bin, args...)
	cmd.Stdout = sink
	cmd.Stderr = sink
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil // nonzero exit is a REAL check outcome, not a substrate error
	}
	return code, err
}

func (p *RealExecProvider) infraFailure(job domain.Job, startedAt time.Time, err error) domain.Outcome {
	logs := []byte(fmt.Sprintf("realexec infra failure: %v\n", redactString(err.Error())))
	fallback := CheckResult{
		Tool: "container", Verdict: verdictFailed, Executed: true,
		Detail: sanitizeTail(err.Error()),
	}
	durationMS := p.nowFn().Sub(startedAt).Milliseconds()
	census := domain.TestResults{Total: 1, Failed: 1}
	return domain.Outcome{
		Status: domain.StatusFailed, Classification: "exit_nonzero",
		Logs: logs, DurationMS: durationMS,
		CostMilliCents: costFromDuration(time.Duration(durationMS) * time.Millisecond),
		Results:        &census,
		Artifacts:      []domain.Artifact{LogsArtifact(logs), ChecksArtifact([]CheckResult{fallback})},
	}
}

// finish assembles the contract-complete Outcome using the same fields sim/
// docker populate (status/logs/artifacts/duration/cost/results census), so
// completion payloads and the evidence engine stay untouched.
func (p *RealExecProvider) finish(startedAt, deadline time.Time, status, classification string, checks []CheckResult, logs []byte) domain.Outcome {
	if p.nowFn().After(deadline) && status == domain.StatusSucceeded {
		// Defensive: work landing after the deadline cannot claim a clean
		// verdict — deadline polls have already ruled timed_out for it.
		status, classification = domain.StatusTimedOut, "timeout"
	}
	durationMS := p.nowFn().Sub(startedAt).Milliseconds()
	census := CensusFor(checks)
	return domain.Outcome{
		Status: status, Classification: classification,
		Logs:           logs,
		DurationMS:     durationMS,
		CostMilliCents: costFromDuration(time.Duration(durationMS) * time.Millisecond),
		Results:        &census,
		Artifacts:      []domain.Artifact{LogsArtifact(logs), ChecksArtifact(checks)},
	}
}

// classifyRun collapses executed verdicts onto job status. Only REAL executed
// failures fail a job; skips never flip status nor fabricate evidence.
func classifyRun(now, deadline time.Time, checks []CheckResult) (string, string) {
	if now.After(deadline) {
		return domain.StatusTimedOut, "timeout"
	}
	for _, c := range checks {
		if c.Executed && c.Verdict == verdictFailed {
			return domain.StatusFailed, "exit_nonzero"
		}
	}
	return domain.StatusSucceeded, ""
}

func redactString(s string) string { return redact.String(s) }
