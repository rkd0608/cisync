package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestHookBarePathAlias pins the GitHub-facing route: deliveries arrive at
// POST /hooks/github (the conventional webhook path used by external
// producers), while /v1/hooks/github remains the versioned alias.
func TestHookBarePathAlias(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/hooks/github", "/v1/hooks/github"} {
		req, _ := http.NewRequest(http.MethodPost, h.svc.URL+path, strings.NewReader(`{"action":"opened"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", signBody([]byte(h.secret), []byte(`{"action":"opened"}`)))
		req.Header.Set("X-GitHub-Delivery", "route-"+path)
		req.Header.Set("X-GitHub-Event", "push")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %s = %d, want 202", path, resp.StatusCode)
		}
	}
}
