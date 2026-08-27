package materialize

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Materializer downloads GitHub head-state archives with the control-plane's
// OWN installation credentials (the control-plane is the evidence authority,
// ARCHITECTURE §5.2) and stages them under a shared repos volume keyed by
// inputs_hash. Runners receive ONLY staged file paths — job-lease workers
// never hold GitHub tokens (THREAT_MODEL B5: no credentials in sandboxes).
//
// Cache honesty: inputs_hash covers base/head SHA, lockfiles, flags and
// toolchain (I-02), so an existing stage for that key IS the identical tree
// snapshot; reuse is byte-for-byte and never silently widened.
type Materializer struct {
	dir      string // staging dir, shared to fleet via cisync-repos volume
	source   TokenSource
	apiBase  string // e.g. https://api.github.com/
	client   *http.Client
	maxBytes int64 // archive download cap (tar-bomb guard upstream of extract)
}

// DefaultMaxArchiveBytes caps materialized snapshots at 256 MiB (v0).
const DefaultMaxArchiveBytes = 256 << 20

// New builds a materializer staging under dir (created eagerly so boot fails
// fast on unwritable volumes).
func New(dir string, source TokenSource) (*Materializer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("materialize: stage dir %q: %w", dir, err)
	}
	return &Materializer{
		dir:      dir,
		source:   source,
		apiBase:  "https://api.github.com/",
		client:   &http.Client{},
		maxBytes: DefaultMaxArchiveBytes,
	}, nil
}

// SetAPIBase overrides the GitHub API root (tests / GH Enterprise instances).
func (m *Materializer) SetAPIBase(base string) {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	m.apiBase = base
}

// Materialize returns the absolute staged path for repo@headSHA keyed by
// inputs_hash. It is safe for concurrent dispatchers: staging writes land in
// a unique temp file renamed atomically into place.
func (m *Materializer) Materialize(ctx context.Context, repo, headSHA, inputsHash string) (string, error) {
	hex := strings.TrimPrefix(inputsHash, "sha256:")
	if !isHex64(hex) {
		return "", fmt.Errorf("materialize: unsafe inputs_hash %q", redactHash(inputsHash))
	}
	final := filepath.Join(m.dir, hex+".tar.gz")
	if info, err := os.Stat(final); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return final, nil // I-02 cache hit: same key ⇒ same tree snapshot
	}

	token, err := m.source.InstallationToken(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("materialize: token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%srepos/%s/tarball/%s", m.apiBase, repo, headSHA), nil)
	if err != nil {
		return "", fmt.Errorf("materialize: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("materialize: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("materialize: fetch archive status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(m.dir, "*.tmp")
	if err != nil {
		return "", fmt.Errorf("materialize: tmpfile: %w", err)
	}
	tmpName := tmp.Name()
	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, m.maxBytes+1))
	closeErr := tmp.Close()
	switch {
	case written > m.maxBytes:
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("materialize: archive exceeds cap %d bytes", m.maxBytes)
	case copyErr != nil:
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("materialize: write archive: %w", copyErr)
	case closeErr != nil:
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("materialize: flush archive: %w", closeErr)
	}
	if renameErr := os.Rename(tmpName, final); renameErr != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("materialize: commit archive: %w", renameErr)
	}
	return final, nil
}

// isHex64 accepts exactly lowercase sha256 hex (traversal-safe staging keys).
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func redactHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	if len(h) == 0 {
		return "<empty>"
	}
	return h[:len(h)/2] + "…"
}
