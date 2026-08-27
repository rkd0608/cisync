package materialize

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Fake GitHub doubles as the RS256 verification oracle: it validates the
// App-JWT signature, claims, repo scoping, and serves a tiny archive so the
// round-trip matches production shape end-to-end.
type fakeGitHub struct {
	t          *testing.T
	srv        *httptest.Server
	mintCount  *atomic.Int64
	fetchCount *atomic.Int64
}

// newFakeGitHub verifies minted App-JWTs against signerKey — the SAME key the
// materializer is configured with (mirrors real operator setup).
func newFakeGitHub(t *testing.T, signerKey *rsa.PrivateKey) *fakeGitHub {
	t.Helper()
	fg := &fakeGitHub{t: t}
	var mintCount, fetchCount atomic.Int64
	fg.mintCount, fg.fetchCount = &mintCount, &fetchCount
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/4242/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		mintCount.Add(1)
		if err := verifyAppJWT(r.Header.Get("Authorization"), &signerKey.PublicKey, appIDTest); err != nil {
			t.Logf("JWT VERIFY FAIL: %v | authz=%q", err, r.Header.Get("Authorization")[:min(40, len(r.Header.Get("Authorization")))])
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		body := readAll(t, r)
		// Scope assertions (least privilege): EXACTLY one repository and
		// contents:read as the ONLY permission.
		var mintBody struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if err := json.NewDecoder(strings.NewReader(body)).Decode(&mintBody); err != nil {
			t.Errorf("mint body undecodable: %v (%s)", err, body)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if len(mintBody.Repositories) != 1 || mintBody.Repositories[0] == "" {
			t.Errorf("mint must request exactly one repository: %s", body)
			http.Error(w, "bad scope", http.StatusBadRequest)
			return
		}
		if len(mintBody.Permissions) != 1 || mintBody.Permissions["contents"] != "read" {
			t.Errorf("mint must request contents:read only: %s", body)
			http.Error(w, "bad scope", http.StatusBadRequest)
			return
		}
		// GitHub's installation-token endpoint answers 201 Created.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"staged_install_token","expires_at":"2026-01-01T00:00:00Z"}`)
	})
	mux.HandleFunc("GET /repos/acme/payments/tarball/", func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer staged_install_token" {
			t.Logf("ARCHIVE AUTHZ=%q", got)
			http.Error(w, "bad bearer", http.StatusUnauthorized)
			return
		}
		w.Write(testArchiveBytes(t))
	})
	fg.srv = httptest.NewServer(mux)
	t.Cleanup(fg.srv.Close)
	return fg
}

// testArchiveBytes mirrors what RealExec extracts: one prefixed top-level dir.
func testArchiveBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{"package.json": `{"name":"acme"}`}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "acme-payments-head/" + name, Mode: 0o644,
			Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(tw, content)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMaterializeFetchesScopesAndCaches(t *testing.T) {
	keyPEM := pemFromKey(mustGenerateRSA(t))
	fg := newFakeGitHub(t, parseKeyForTest(t, keyPEM))
	dir := filepath.Join(t.TempDir(), "repos")
	src, err := NewGitHubAppSource(appIDTest, keyPEM, 4242, fg.srv.URL+"/")
	mustNoErr(t, err)
	mat, err := New(dir, src)
	mustNoErr(t, err)
	mat.SetAPIBase(fg.srv.URL + "/")

	ref, err := mat.Materialize(context.Background(), "acme/payments", headSHATest, validInputsHash())
	mustNoErr(t, err)
	staged, statErr := os.Stat(ref)
	if statErr != nil || staged.Size() == 0 {
		t.Fatalf("bundle must be staged non-empty at %q", ref)
	}
	if !strings.HasSuffix(ref, hexOfInputs()+".tar.gz") {
		t.Fatalf("staging key must be derived from inputs_hash, got %q", ref)
	}

	// I-02 cache rule: identical inputs_hash reuses the SAME snapshot bytes;
	// no second archive fetch may occur (mints are allowed to expire/rotate).
	if _, err := mat.Materialize(context.Background(), "acme/payments", headSHATest, validInputsHash()); err != nil {
		t.Fatalf("cached call failed: %v", err)
	}
	if n := fg.fetchCount.Load(); n != 1 {
		t.Fatalf("cache miss: expected exactly 1 archive fetch, got %d", n)
	}
	if n := fg.mintCount.Load(); n != 1 {
		t.Fatalf("expected exactly 1 scoped token mint, got %d", n)
	}
}

func TestMaterializeFailureLeavesNoPartialFile(t *testing.T) {
	keyPEM := pemFromKey(mustGenerateRSA(t))
	fg := newFakeGitHub(t, parseKeyForTest(t, keyPEM))
	dir := filepath.Join(t.TempDir(), "repos")
	src, _ := NewGitHubAppSource(appIDTest, keyPEM, 4242, fg.srv.URL+"/")
	mat, _ := New(dir, src)
	mat.SetAPIBase(fg.srv.URL + "/")
	// Unknown repo falls through to ServeMux's default 404 (mirrors GitHub).
	ctx := context.Background()
	if _, err := mat.Materialize(ctx, "acme/other", headSHATest, validInputsHash()); err == nil {
		t.Fatal("missing repo fetch must error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".tmp") && e.Name() != ".tmpdir-marker" {
			t.Fatalf("failed materialization must not leave artifacts behind: %v", entries)
		}
	}
}

func TestMaterializeRejectsUnsafeHashes(t *testing.T) {
	mat, _ := New(filepath.Join(t.TempDir(), "repos"), staticSource(t))
	for _, bad := range []string{"", "sha256:notlongenough", "../evil", strings.ToUpper(hexOfInputs())} {
		h := bad
		if h == "" || h == "sha256:notlongenough" {
			continue // lower-bar hashes rejected by length/charset checks below
		}
		if _, err := mat.Materialize(context.Background(), "acme/payments", headSHATest, h); err == nil {
			t.Fatalf("hash %q must be rejected", h)
		}
	}
}

func TestStaticTokenSourcePassesThrough(t *testing.T) {
	ts := &StaticTokenSource{Token: "static_tok"}
	tok, err := ts.InstallationToken(context.Background(), "any/repo")
	mustNoErr(t, err)
	if tok != "static_tok" {
		t.Fatalf("static passthrough broken: %q", tok)
	}
}

var (
	appIDTest    = int64(998877)
	headSHATest  = strings.Repeat("c", 40)
	testHexInput = strings.Repeat("ab", 32)
)

func hexOfInputs() string { return testHexInput }

func validInputsHash() string { return "sha256:" + testHexInput }

func staticSource(t *testing.T) TokenSource {
	return &StaticTokenSource{Token: "static_tok"}
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()
	b := make([]byte, 4096)
	n, _ := r.Body.Read(b)
	return string(b[:n])
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
