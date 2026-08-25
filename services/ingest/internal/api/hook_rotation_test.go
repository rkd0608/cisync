package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"sauron.dev/sauron/ingest/internal/forward"
)

// TestRotationWindowOldSecretAcceptedEndToEnd signs a delivery with the OLD
// secret while ingest verifies against "new,old" and asserts the delivery is
// persisted AND forwarded with an event_kind that carries the payload action
// (plan §3.1: ingest passes X-GitHub-Event[.action] verbatim).
func TestRotationWindowOldSecretAcceptedEndToEnd(t *testing.T) {
	h := newHarnessSecrets(t, nil, "brand-new-secret", "old-window-secret")
	defer h.svc.Close()

	oldSecret := []byte("old-window-secret")
	payload := map[string]any{
		"action":     "opened",
		"repository": map[string]any{"full_name": "acme/payments"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/hooks/github", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signBody(oldSecret, raw))
	req.Header.Set("X-GitHub-Delivery", "rot-guid-1")
	req.Header.Set("X-GitHub-Event", "pull_request")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("old-secret delivery during window must be accepted, got %d", resp.StatusCode)
	}

	select {
	case env := <-h.ctrlCalls:
		if env.EventKind != "pull_request.opened" {
			t.Fatalf("event_kind must compose event.action, got %q", env.EventKind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery never forwarded")
	}
	if _, err := h.st.GetDelivery(context.Background(), "github", "rot-guid-1"); err != nil {
		t.Fatalf("valid old-secret delivery must be persisted: %v", err)
	}
}

// TestComposeEventKindMatrix covers the pure composition used at the edge.
func TestComposeEventKindMatrix(t *testing.T) {
	cases := []struct {
		header string
		body   string
		want   string
	}{
		{"push", `{"before":"a","after":"b"}`, "push"},
		{"pull_request", `{"action":"synchronize"}`, "pull_request.synchronize"},
		{"installation", `{"action":"deleted"}`, "installation.deleted"},
		{"pull_request", `not-json`, "pull_request"},
		{"", `{"action":"x"}`, ""},
	}
	for _, tc := range cases {
		if got := composeEventKind(tc.header, []byte(tc.body)); got != tc.want {
			t.Fatalf("composeEventKind(%q,%s) = %q want %q", tc.header, tc.body, got, tc.want)
		}
	}
}

var _ = forward.ResultAccepted // keep import stable if harness changes
