package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
)

func TestHookValidSignatureAcceptedAndForwarded(t *testing.T) {
	h := newHarness(t, nil)
	body := validPayload()
	resp := h.post("d-1", "pull_request", body, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	select {
	case env := <-h.ctrlCalls:
		if env.Source != "github" || env.ExtDeliveryID != "d-1" || env.EventKind != "pull_request" {
			t.Fatalf("unexpected envelope: %+v", env)
		}
		if env.Repo != "acme/payments" {
			t.Fatalf("repo not extracted: %+v", env)
		}
		if strings.Contains(string(env.Payload), "ghs_dontleakme123456") {
			t.Fatalf("secret leaked to control-plane: %s", env.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("control-plane never received the delivery")
	}

	d, err := h.st.GetDelivery(context.Background(), "github", "d-1")
	if err != nil {
		t.Fatalf("delivery not persisted: %v", err)
	}
	if d.Status != domain.StatusForwarded {
		t.Fatalf("expected forwarded, got %s", d.Status)
	}
	if !strings.Contains(string(d.Payload), "ghs_dontleakme123456") {
		t.Fatalf("raw payload must be stored unredacted for audit")
	}
}

func TestHookDuplicateDeliveryIdempotent(t *testing.T) {
	var calls int
	var mu sync.Mutex
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		readEnvelope(t, r)
		w.WriteHeader(http.StatusAccepted)
	})
	body := validPayload()
	first := h.post("dup-1", "push", body, nil)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first delivery must 202, got %d", first.StatusCode)
	}
	second := h.post("dup-1", "push", body, nil)
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("duplicate must still 202 (idempotent), got %d", second.StatusCode)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("control-plane never called")
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("duplicate must not forward twice, got %d forwards", n)
	}
	counts, _ := h.st.CountByStatus(context.Background())
	total := counts[domain.StatusPending] + counts[domain.StatusForwarded] + counts[domain.StatusForwardFailed]
	if total != 1 {
		t.Fatalf("exactly one row expected, got %d (%v)", total, counts)
	}
}

func TestHookControlPlaneUnavailable503WithRetryAfter(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	body := validPayload()
	resp := h.post("unavail-1", "push", body, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ctrl unavailable must surface 503, got %d", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Fatalf("Retry-After header required for GitHub redelivery")
	}
	d, err := h.st.GetDelivery(context.Background(), "github", "unavail-1")
	if err != nil {
		t.Fatalf("delivery must be persisted before forwarding: %v", err)
	}
	if d.Status != domain.StatusPending {
		t.Fatalf("delivery must stay pending, got %s", d.Status)
	}
	if d.Attempts != 1 {
		t.Fatalf("first failed attempt recorded, attempts=%d", d.Attempts)
	}
}

func TestHookControlPlanePermanentRejectionDefersTo202(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	resp := h.post("rej-1", "push", validPayload(), nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid-signature delivery must 202 even when ctrl rejects permanently, got %d", resp.StatusCode)
	}
	d, _ := h.st.GetDelivery(context.Background(), "github", "rej-1")
	if d.Status != domain.StatusForwardFailed {
		t.Fatalf("permanent rejection must mark forward_failed, got %s", d.Status)
	}
}
