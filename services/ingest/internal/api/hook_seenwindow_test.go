package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"sauron.dev/sauron/ingest/internal/domain"
)

// signedPost posts a validly-signed webhook with explicit GUID and payload.
func signedPost(t *testing.T, h *harness, deliveryID, eventKind string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signBody([]byte(h.secret), body))
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-GitHub-Event", eventKind)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestReplaySeenWindowSameContentDifferentGUID pins H2: identical content
// under a FRESH GUID inside the window is ACCEPTED (202) but durably marked
// duplicate_suspect and forwarded record-only; ctrl-side effects are gated
// by the flag, never by rejection.
func TestReplaySeenWindowSameContentDifferentGUID(t *testing.T) {
	h := newHarness(t, nil)

	if code := signedPost(t, h, "guid-first-1", "push", validPayload()); code != http.StatusAccepted {
		t.Fatalf("first sighting = %d, want 202", code)
	}
	envs := waitForEnvelopes(t, h, 1)
	if envs[0].DuplicateSuspect {
		t.Fatalf("first sighting must forward unflagged: %+v", envs[0])
	}

	if code := signedPost(t, h, "guid-fresh-2", "push", validPayload()); code != http.StatusAccepted {
		t.Fatalf("duplicate-suspect sighting must still be ACCEPTED, got %d", code)
	}
	envs = waitForEnvelopes(t, h, 1)
	if !envs[0].DuplicateSuspect {
		t.Fatalf("same content under fresh GUID must carry duplicate_suspect flag: %+v", envs[0])
	}
	d, err := h.st.GetDelivery(context.Background(), "github", "guid-fresh-2")
	if err != nil {
		t.Fatal(err)
	}
	if !d.DuplicateSuspect {
		t.Fatal("duplicate_suspect diagnosis must persist on the delivery row")
	}
	if d.Status == domain.StatusRejected {
		t.Fatal("suspects are accepted traffic, never rejected")
	}
	assertNoEnvelopes(t, h)
}

// TestReplaySeenWindowDifferentContentUnchanged pins that different content
// under ANY GUID keeps the exact legacy behavior (no flag).
func TestReplaySeenWindowDifferentContentUnchanged(t *testing.T) {
	h := newHarness(t, nil)

	first := []byte(`{"action":"opened","repository":{"full_name":"acme/payments"},"number":7}`)
	second := []byte(`{"action":"opened","repository":{"full_name":"acme/payments"},"number":8}`)
	third := []byte(`{"action":"opened","repository":{"full_name":"acme/payments"},"number":9}`)
	guids := []string{"guid-variant", "guid-variant-a", "guid-variant-b"}
	bodies := [][]byte{first, second, third}

	for i, body := range bodies {
		if code := signedPost(t, h, guids[i], "pull_request", body); code != http.StatusAccepted {
			t.Fatalf("case %d: distinct content must behave normally, got %d", i, code)
		}
	}
	envs := waitForEnvelopes(t, h, 3)
	for _, env := range envs {
		if env.DuplicateSuspect {
			t.Fatalf("different-content deliveries must never be flagged: %+v", env)
		}
	}
	for _, id := range []string{"guid-variant", "guid-variant-a", "guid-variant-b"} {
		d, err := h.st.GetDelivery(context.Background(), "github", id)
		if err != nil {
			t.Fatalf("delivery %s must persist: %v", id, err)
		}
		if d.DuplicateSuspect {
			t.Fatalf("distinct-content delivery %s must not be flagged", id)
		}
	}
}
