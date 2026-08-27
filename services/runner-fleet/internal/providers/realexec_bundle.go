package providers

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cisync.dev/cisync/runner-fleet/internal/domain"
	"cisync.dev/cisync/runner-fleet/internal/redact"
)

// realexec_bundle.go maps job_spec bundle refs onto the staged tarball and
// unpacks it under strict guards (zip-slip, bomb cap).

// maxExtractedBytes bounds archive extraction (tar-bomb guard).
const maxExtractedBytes = 512 << 20

// resolveBundle maps job_spec.pre_fetched_bundle_ref onto a readable local
// tarball. Refs are absolute staged paths under the shared cisync-repos
// volume; a bare inputs-hash fallback covers specs dispatched before ctrl
// stamped the resolved path. The second return is the honest skip reason.
func (p *RealExecProvider) resolveBundle(spec domain.JobSpec) (string, string) {
	ref := strings.TrimSpace(spec.PreFetchedBundleRef)
	if ref == "" {
		return "", "control-plane dispatched without pre_fetched_bundle_ref (materializer disabled or failed)"
	}
	if info, err := os.Stat(ref); err == nil && info.Mode().IsRegular() {
		return ref, ""
	}
	if hex := strings.TrimPrefix(spec.InputsHash, "sha256:"); isHex64(hex) {
		candidate := filepath.Join(p.ReposDir, hex+".tar.gz")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, ""
		}
	}
	return "", fmt.Sprintf("ref %q did not resolve to a staged tarball under %q",
		redact.String(ref), p.ReposDir)
}

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

// extractTarball unpacks a .tar.gz bundle into workspace with zip-slip and
// size-cap guards; it returns the count of extracted entries. Malformed or
// hostile archives are rejected here and surface upstream as a skip-with-
// reason outcome — never as fabricated check results.
func extractTarball(bundlePath, workspace string) (int, error) {
	raw, err := os.Open(bundlePath)
	if err != nil {
		return 0, fmt.Errorf("open bundle: %w", err)
	}
	defer raw.Close()
	gz, err := gzip.NewReader(raw)
	if err != nil {
		return 0, fmt.Errorf("bundle must be gzip tar (%v)", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return 0, err
	}
	total, count := int64(0), 0
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return count, fmt.Errorf("read archive: %w", err)
		}
		if header.Typeflag == tar.TypeDir {
			continue // directories materialize via file entries below
		}
		target := filepath.Join(workspace, header.Name)
		cleanedTarget, cleanedRoot := filepath.Clean(target), filepath.Clean(workspace)+string(os.PathSeparator)
		if !strings.HasPrefix(cleanedTarget, cleanedRoot) && cleanedTarget != filepath.Clean(workspace) {
			return count, fmt.Errorf("archive entry escapes workspace: %q", redact.String(header.Name))
		}
		total += header.Size
		if total > maxExtractedBytes {
			return count, fmt.Errorf("archive exceeds extraction cap of %d bytes", maxExtractedBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, safeMode(header.FileInfo()))
		if err != nil {
			return count, fmt.Errorf("extract entry: %w", err)
		}
		copyErr := func() error { _, err := io.Copy(out, reader); return err }()
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			return count, fmt.Errorf("copy entry failed")
		}
		count++
	}
}

func safeMode(info os.FileInfo) os.FileMode {
	mode := info.Mode().Perm()
	// Ensure owner-readable so the non-root sandbox user (65534) can consume
	// the ro-mounted sources regardless of archive modes.
	if mode&0o400 == 0 {
		mode |= 0o400
	}
	return mode
}
