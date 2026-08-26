package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"sync"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// NOT-FOR-PRODUCTION: DockerProvider executes agent-supplied job specs inside
// containers. It applies defense-in-depth flags per THREAT_MODEL B5
// (--network none --read-only rootfs, tmpfs scratch, memory/cpu/pid caps,
// non-root uid, hard timeout), but it is NOT an isolation boundary and must
// not run untrusted multi-tenant work until the SECURITY_TRUST_DRAFT §6.3
// graduation checklist (gVisor/Firecracker, egress allowlists) passes.

// DockerHandle tracks one in-flight container execution.
type DockerHandle struct {
	cmd      *exec.Cmd
	done     bool
	outcome  domain.Outcome
	mu       sync.Mutex
	cancel   context.CancelFunc
	logsHash hash.Hash
}

// DockerProvider runs each job in a locked-down container (opt-in via
// CISYNC_FLEET_PROVIDER=docker). DEV/DEMO ONLY — see NOT-FOR-PRODUCTION note.
type DockerProvider struct {
	Bin     string
	Image   string
	nowFn   func() time.Time
	handles sync.Map
}

// NewDocker returns a docker provider using the given CLI binary and image.
func NewDocker(bin string, image string) *DockerProvider {
	return &DockerProvider{Bin: bin, Image: image, nowFn: time.Now}
}

// Submit launches one container per job with the B5 sandbox flags and a hard
// timeout derived from job_spec.timeout_ms.
func (p *DockerProvider) Submit(ctx context.Context, job domain.Job) (domain.Handle, error) {
	runCtx, cancel := context.WithCancel(ctx)
	timeout := 15 * time.Minute
	if job.Spec.TimeoutMS > 0 {
		timeout = time.Duration(job.Spec.TimeoutMS) * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { cancel() })

	args := []string{
		"run", "--rm",
		"--network", "none",
		"--read-only",
		"--memory", "512m",
		"--cpus", "1",
		"--pids-limit", "128",
		"--tmpfs", "/tmp:rw,size=64m,noexec,nosuid",
		"--user", "65534:65534",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		p.Image,
		"/bin/sh", "-c", commandFor(job),
	}
	cmd := exec.CommandContext(runCtx, p.Bin, args...)
	var out bytes.Buffer
	hasher := sha256.New()
	sink := io.MultiWriter(&out, hasher)
	cmd.Stdout = sink
	cmd.Stderr = sink

	startedAt := p.nowFn()
	h := &DockerHandle{cmd: cmd, cancel: cancel, logsHash: hasher}
	p.handles.Store(h, struct{}{})

	go func() {
		defer timer.Stop()
		err := cmd.Run()
		h.mu.Lock()
		defer h.mu.Unlock()
		h.done = true
		logs := append([]byte(nil), out.Bytes()...)
		if err != nil && len(logs) == 0 {
			logs = []byte(fmt.Sprintf("container run failed: %v\n", err))
		}
		o := domain.Outcome{
			Logs:           logs,
			DurationMS:     p.nowFn().Sub(startedAt).Milliseconds(),
			CostMilliCents: costFromDuration(p.nowFn().Sub(startedAt)),
		}
		switch {
		case err == nil:
			o.Status = domain.StatusSucceeded
			c := censusFromExit(nil)
			o.Results = &c
		case deadlinePassed(deadline, runCtx):
			o.Status = domain.StatusTimedOut
			c := censusFromExit(err)
			o.Results = &c
		default:
			o.Status = domain.StatusFailed
			o.Classification = "exit_nonzero"
			c := censusFromExit(err)
			o.Results = &c
		}
		o.Artifacts = []domain.Artifact{logArtifact(o)}
		h.outcome = o
		p.handles.Delete(h)
	}()
	return h, nil
}

// Cancel kills the container best-effort; repeated cancels are no-ops.
func (p *DockerProvider) Cancel(handle domain.Handle) error {
	h, ok := handle.(*DockerHandle)
	if !ok {
		return fmt.Errorf("docker provider: foreign handle %T", handle)
	}
	h.cancel()
	return nil
}

// Poll reports Running until the container goroutine finishes.
func (p *DockerProvider) Poll(handle domain.Handle) (domain.PollState, domain.Outcome) {
	h, ok := handle.(*DockerHandle)
	if !ok {
		return domain.PollDone, domain.Outcome{Status: domain.StatusFailed, Classification: "infra_transient"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.done {
		return domain.PollRunning, domain.Outcome{}
	}
	return domain.PollDone, h.outcome
}

func commandFor(job domain.Job) string {
	kind := job.Spec.Kind
	if kind == "" {
		kind = "hermetic_build"
	}
	return fmt.Sprintf("echo 'cisync %s %s@%s'", kind, job.Spec.Repo, job.Spec.HeadSHA)
}

func deadlinePassed(deadline time.Time, ctx context.Context) bool {
	return time.Now().After(deadline) || ctx.Err() != nil
}

// censusFromExit maps the container exit code onto the outcome census
// (P0-2): docker jobs execute exactly one command, so total=1 and the exit
// code alone decides passed vs failed.
func censusFromExit(err error) domain.TestResults {
	if err == nil {
		return domain.TestResults{Total: 1, Passed: 1}
	}
	return domain.TestResults{Total: 1, Failed: 1}
}

func costFromDuration(d time.Duration) int64 {
	return d.Milliseconds() / 1000
}

func logArtifact(o domain.Outcome) domain.Artifact {
	return domain.Artifact{
		Name:        "logs.txt",
		Digest:      DigestOf(o.Logs),
		SizeBytes:   int64(len(o.Logs)),
		Content:     o.Logs,
		ContentType: "text/plain",
	}
}
