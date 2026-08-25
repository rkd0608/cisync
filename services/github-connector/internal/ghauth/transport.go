package ghauth

import (
	"net/http"
	"os"
	"strings"
)

func readFileForTestability(path string) ([]byte, error) { return os.ReadFile(path) }

// repoScopedTransport injects a per-repo-scoped installation token on every
// GitHub API call. The repo is recovered from the request path
// (/repos/{owner}/{name}/…), which is the only scope the Checks API calls
// need — this is what makes per-repo token narrowing possible at transport
// level without threading repo through every go-github call site.
type repoScopedTransport struct {
	registry       *Registry
	installationID int64
}

// RoundTrip implements http.RoundTripper.
func (t *repoScopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	src, err := t.registry.Source(t.installationID)
	if err != nil {
		return nil, err
	}
	token, err := src.Token(repoFromPath(req.URL.Path))
	if err != nil {
		return nil, err
	}
	// Clone to avoid mutating the caller's request headers.
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultTransport.RoundTrip(cloned)
}

// repoFromPath extracts "owner/name" from /repos/{owner}/{name}/…
// returning "" for non-repo-scoped paths (callers mint unscoped then; the
// connector only ever calls repo-scoped endpoints today).
func repoFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/repos/")
	if rest == path {
		return ""
	}
	owner, tail, ok := strings.Cut(rest, "/")
	if !ok || owner == "" {
		return ""
	}
	name, _, _ := strings.Cut(tail, "/")
	if name == "" {
		return ""
	}
	return owner + "/" + name
}
