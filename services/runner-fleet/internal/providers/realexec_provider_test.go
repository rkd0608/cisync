package providers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// fake-docker is a scripted binary standing in for the docker CLI: it records
// its argv for sandbox-flag assertions and behaves per CISYNC_FAKE_DOCKER_MODE
// (pass | partial | unexplained | hang | crash). No daemon required.
func writeFakeDocker(t *testing.T, dir string) string {
	t.Helper()
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$*" >> "` + filepath.Join(dir, "argv.log") + `"` + "\n" +
		`case "${CISYNC_FAKE_DOCKER_MODE:-}" in` + "\n" +
		"  pass)\n" +
		`    echo '[cisync-check] {"tool":"compileall","verdict":"pass","duration_ms":7}'` + "\n" +
		"    echo '[cisync-tail] compileall|all modules compiled'\n" +
		`    echo '[cisync-check] {"tool":"tsc","verdict":"pass","duration_ms":9}'` + "\n" +
		"    exit 0 ;;\n" +
		"  partial)\n" +
		`    echo '[cisync-check] {"tool":"eslint","verdict":"fail","duration_ms":31}'` + "\n" +
		`    echo '[cisync-tail] eslint|token ghp_Aa1Bb2Cc3Dd4Ee5Ff6 apikey=supersecret'` + "\n" +
		"    exit 1 ;;\n" +
		"  unexplained) exit 3 ;;\n" +
		"  hang) sleep 30; exit 0 ;;\n" +
		"  *) exit 71 ;;\n" +
		"esac\n"
	path := filepath.Join(dir, "fake-docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("CISYNC_FAKE_DOCKER_MODE", mode)
}

// pollUntil runs a bounded poll loop so tests never deadlock on slow CI.
func pollUntil(t *testing.T, p *RealExecProvider, h domain.Handle) domain.Outcome {
	t.Helper()
	for i := 0; i < 600; i++ {
		state, oc := p.Poll(h)
		if state == domain.PollDone {
			return oc
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("provider never reached PollDone")
	return domain.Outcome{}
}

func materializeBundle(t *testing.T, reposDir, hashHex string, files map[string]string) {
	t.Helper()
	bundle := buildTestTarball(t, files)
	if err := os.WriteFile(filepath.Join(reposDir, hashHex+".tar.gz"), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRealexecJob(bundleRef string) domain.Job {
	return domain.Job{
		RunID:   "run-realexec-1",
		Attempt: 1,
		Pool:    "sim",
		Spec: domain.JobSpec{
			Kind:                "hermetic_build",
			Repo:                "acme/payments",
			HeadSHA:             strings.Repeat("a", 40),
			InputsHash:          "sha256:" + strings.Repeat("b", 64),
			PreFetchedBundleRef: bundleRef,
			TimeoutMS:           30000,
		},
	}
}

func TestRealExecHappyPathMapsEnvelope(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{
		"package.json": `{"name":"x"}`,
	})
	fakeMode(t, "pass")
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	outcome := pollUntil(t, p, mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz"))))

	if outcome.Status != domain.StatusSucceeded || outcome.Results == nil {
		t.Fatalf("happy path must succeed with census, got %+v", outcome.Status)
	}
	if outcome.Results.Total != 2 || outcome.Results.Passed != 2 {
		t.Fatalf("census must carry two real passes: %+v", *outcome.Results)
	}
	names := artifactNames(outcome.Artifacts)
	for _, want := range []string{"logs.txt", ChecksArtifactName} {
		if !names[want] {
			t.Fatalf("missing artifact %s in %v", want, names)
		}
	}
	// Sandbox flags are part of B5 trust posture — assert the invocation.
	argv, _ := os.ReadFile(filepath.Join(dir, "argv.log"))
	for _, flag := range []string{"--network", "none", "--read-only", "--memory", "--cap-drop"} {
		if !strings.Contains(string(argv), flag) {
			t.Fatalf("sandbox flags missing %q in %q", flag, string(argv))
		}
	}
	checks := decodeChecksArtifact(t, outcome.Artifacts)
	if len(checks) != 2 || checks[0].Tool != "compileall" || checks[1].Tool != "tsc" {
		t.Fatalf("per-check results must be preserved: %+v", checks)
	}
	if checks[0].Detail == "" || checks[0].DurationMS != 7 {
		t.Fatalf("tail and duration must survive parsing: %+v", checks[0])
	}
}

func TestRealExecPartialFailureHonestEnvelope(t *testing.T) {
	dir := t.TempDir()
	reposDir := newReposDir(t)
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{"app.py": "print(1)", "requirements.txt": ""})
	fakeMode(t, "partial")
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	outcome := pollUntil(t, p, mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz"))))

	if outcome.Status != domain.StatusFailed {
		t.Fatalf("partial failure must end failed, got %s", outcome.Status)
	}
	census := *outcome.Results
	if census.Total != 1 || census.Failed != 1 || census.Passed != 0 {
		t.Fatalf("census must show the single executed failure: %+v", census)
	}
	// Sanitization on stored artifacts (B3): the PAT-looking token + kv-secret
	// tail must be scrubbed before persisting evidence bytes.
	logs := readArtifact(t, outcome.Artifacts, "logs.txt")
	for _, forbidden := range []string{"ghp_Aa1Bb2Cc3Dd4Ee5Ff6", "supersecret"} {
		if strings.Contains(string(logs), forbidden) {
			t.Fatalf("secret pattern %q survived redaction", forbidden)
		}
	}
}

func TestRealExecUnexplainedExitFailsClosedWithStack(t *testing.T) {
	dir := t.TempDir()
	reposDir := newReposDir(t)
	materializeBundle(t, reposDir, strings.Repeat("b", 64), map[string]string{"package.json": "{}"})
	fakeMode(t, "unexplained")
	p := NewRealExec(writeFakeDocker(t, dir), "tools:test", "", reposDir)
	outcome := pollUntil(t, p, mustSubmit(t, p, newRealexecJob(filepath.Join(reposDir, strings.Repeat("b", 64)+".tar.gz"))))
	if outcome.Status != domain.StatusFailed || outcome.Results.Failed != 1 || outcome.Results.Passed != 0 {
		t.Fatalf("unexplained exit must become one container failure: %+v / %s", *outcome.Results, outcome.Status)
	}
}

// Missing/unusable bundle ⇒ honest all-skipped job, NOT a fabricated pass or
// a coded failure pretending code was bad. skipped ≠ pass keeps I-01 clean.
func mustSubmit(t *testing.T, p *RealExecProvider, job domain.Job) domain.Handle {
	t.Helper()
	h, err := p.Submit(context.Background(), job)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return h
}

func newReposDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "repos")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func artifactNames(arts []domain.Artifact) map[string]bool {
	m := map[string]bool{}
	for _, a := range arts {
		m[a.Name] = true
	}
	return m
}

func readArtifact(t *testing.T, arts []domain.Artifact, name string) []byte {
	t.Helper()
	for _, a := range arts {
		if a.Name == name {
			return a.Content
		}
	}
	t.Fatalf("artifact %s not found", name)
	return nil
}

func decodeChecksArtifact(t *testing.T, arts []domain.Artifact) []CheckResult {
	t.Helper()
	var out []CheckResult
	if err := json.Unmarshal(readArtifact(t, arts, ChecksArtifactName), &out); err != nil {
		t.Fatalf("checks.json invalid: %v", err)
	}
	return out
}

// buildTestTarball packs files into a gzip tarball mirroring the GitHub
// archive shape the control-plane materializer downloads (single top-level
// entry prefix is tolerated by the extractor).
func buildTestTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "repo-head/" + name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
