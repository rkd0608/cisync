package scheduler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/materialize"
)

// The dispatch→enqueue contract gains pre_fetched_bundle_ref ONLY when
// materialization is enabled and succeeds; disabled/broken materialization
// must never block or poison dispatch (the fleet degrades honestly to skips).
func TestJobSpecWireMappingCarriesPreFetchedRef(t *testing.T) {
	spec := domain.JobSpec{
		Kind:                "hermetic_build",
		Repo:                "acme/payments",
		InputsHash:          "sha256:" + testHex,
		PreFetchedBundleRef: "/repos/" + testHex + ".tar.gz",
	}
	mapped := jobSpecToMap(spec)
	if mapped["pre_fetched_bundle_ref"] != spec.PreFetchedBundleRef {
		t.Fatalf("wire map must carry pre_fetched_bundle_ref: %v", mapped["pre_fetched_bundle_ref"])
	}
}

func TestJobSpecWireMappingOmitsEmptyRef(t *testing.T) {
	mapped := jobSpecToMap(domain.JobSpec{Kind: "hermetic_build", Repo: "acme/payments"})
	if _, present := mapped["pre_fetched_bundle_ref"]; present {
		t.Fatalf("empty ref must stay absent from wire map (omitempty): %v", mapped)
	}
}

// EngineScheduler.materializeFor behavior matrix against a live static-token
// fake GitHub (no App-JWT machinery needed here — token flow covered in the
// materialize package suite).
func TestMaterializeForMatrix(t *testing.T) {
	mat := newTestMaterializer(t)

	t.Run("nil materializer is a silent no-op", func(t *testing.T) {
		e := &EngineScheduler{}
		if got := e.materializeFor(context.Background(), runWithHash(validHash)); got != "" {
			t.Fatalf("disabled materializer must yield no ref, got %q", got)
		}
	})

	t.Run("success yields staged ref", func(t *testing.T) {
		e := &EngineScheduler{materializer: mat}
		got := e.materializeFor(context.Background(), runWithHash(validHash))
		if !strings.HasSuffix(got, strings.TrimPrefix(validHash, "sha256:")+".tar.gz") {
			t.Fatalf("expected staged tarball ref keyed by inputs_hash, got %q", got)
		}
	})

	t.Run("failure yields empty ref without blocking dispatch", func(t *testing.T) {
		e := &EngineScheduler{materializer: mat}
		got := e.materializeFor(context.Background(), runWithHash(unstagedHash))
		if got != "" {
			t.Fatalf("failed materialization must not carry a ref, got %q", got)
		}
	})
}

var (
	testHex      = strings.Repeat("ab", 32)
	validHash    = "sha256:" + testHex
	unstagedHash = "sha256:" + strings.Repeat("cd", 32)
)

func runWithHash(hash string) *domain.ValidationRun {
	// The failure path exercises an unservable HEAD STATE (all-c), while the
	// success path uses the servable head — same repo, orthogonal dimension.
	head := strings.Repeat("e", 40)
	if hash == unstagedHash {
		head = strings.Repeat("c", 40)
	}
	return domain.NewValidationRun("run_mat_1", "tenant", "plan", "cand", 2,
		domain.JobSpec{
			Kind:       "hermetic_build",
			Repo:       "acme/payments",
			HeadSHA:    head,
			InputsHash: hash,
		}, "sim", 1000, 1, 1.0, timeNowUTC())
}

func newTestMaterializer(t *testing.T) *materialize.Materializer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/payments/tarball/") ||
			strings.Contains(r.URL.Path, strings.Repeat("c", 40)) {
			// Non-served repos OR unservable head states mirror GitHub 404.
			http.NotFound(w, r)
			return
		}
		var buf strings.Builder
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		content := `{"name":"acme"}`
		if err := tw.WriteHeader(&tar.Header{Name: "acme-payments/package.json",
			Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Error(err)
			return
		}
		tw.Write([]byte(content))
		tw.Close()
		gz.Close()
		w.Write([]byte(buf.String()))
	}))
	t.Cleanup(srv.Close)

	dir := filepath.Join(t.TempDir(), "repos")
	mat, err := materialize.New(dir, &materialize.StaticTokenSource{Token: "test_tok"})
	if err != nil {
		t.Fatal(err)
	}
	mat.SetAPIBase(srv.URL + "/")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return mat
}
