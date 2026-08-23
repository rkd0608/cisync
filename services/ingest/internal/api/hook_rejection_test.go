package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
)

func TestHookSignatureFailures(t *testing.T) {
	h := newHarness(t, nil)
	body := validPayload()

	tamperedReq, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(`{"action":"closed"}`))
	tamperedReq.Header.Set("Content-Type", "application/json")
	tamperedReq.Header.Set("X-Hub-Signature-256", signBody([]byte(h.secret), body))
	tamperedReq.Header.Set("X-GitHub-Delivery", "d-tamper")
	tamperedReq.Header.Set("X-GitHub-Event", "push")
	respTampered, _ := http.DefaultClient.Do(tamperedReq)
	respTampered.Body.Close()
	if respTampered.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered body must 401, got %d", respTampered.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", signBody([]byte("other-secret"), body))
	req.Header.Set("X-GitHub-Delivery", "d-wrong")
	req.Header.Set("X-GitHub-Event", "push")
	respWrong, _ := http.DefaultClient.Do(req)
	respWrong.Body.Close()
	if respWrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret must 401, got %d", respWrong.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(string(body)))
	req2.Header.Set("X-GitHub-Delivery", "d-missing")
	respMissing, _ := http.DefaultClient.Do(req2)
	respMissing.Body.Close()
	if respMissing.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing signature must 401, got %d", respMissing.StatusCode)
	}

	req3, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(string(body)))
	req3.Header.Set("X-Hub-Signature-256", "sha256=zzzz")
	req3.Header.Set("X-GitHub-Delivery", "d-garbage")
	respGarbage, _ := http.DefaultClient.Do(req3)
	respGarbage.Body.Close()
	if respGarbage.StatusCode != http.StatusUnauthorized {
		t.Fatalf("garbage signature must 401, got %d", respGarbage.StatusCode)
	}

	for _, id := range []string{"d-tamper", "d-wrong", "d-missing", "d-garbage"} {
		d, err := h.st.GetDelivery(context.Background(), "github", id)
		if err != nil {
			t.Fatalf("rejected delivery %s must be persisted as an audit row: %v", id, err)
		}
		if d.SigOK {
			t.Fatalf("rejected delivery %s must be marked sig_ok=false", id)
		}
		if d.Status != domain.StatusRejected {
			t.Fatalf("rejected delivery %s must be quarantined as %q, got %q", id, domain.StatusRejected, d.Status)
		}
	}
	select {
	case <-h.ctrlCalls:
		t.Fatalf("no rejected request may reach control-plane")
	default:
	}

	// A later VALID redelivery of a rejected GUID must not be treated as a
	// duplicate — quarantine rows never occupy the dedup slot.
	okReq, _ := http.NewRequest(http.MethodPost, h.svc.URL+"/hooks/github", strings.NewReader(string(body)))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.Header.Set("X-Hub-Signature-256", signBody([]byte(h.secret), body))
	okReq.Header.Set("X-GitHub-Delivery", "d-tamper")
	okReq.Header.Set("X-GitHub-Event", "push")
	respOK, _ := http.DefaultClient.Do(okReq)
	respOK.Body.Close()
	if respOK.StatusCode != http.StatusAccepted {
		t.Fatalf("valid redelivery after rejection must 202, got %d", respOK.StatusCode)
	}
}

func TestHookTimestampSkewRejected(t *testing.T) {
	h := newHarness(t, nil)
	body := validPayload()

	old := h.now.Add(-6 * time.Minute).Format(time.RFC3339)
	resp := h.post("d-skew-old", "push", body, func(r *http.Request) {
		r.Header.Set("X-Sauron-Timestamp", old)
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale timestamp must 401, got %d", resp.StatusCode)
	}

	fresh := h.now.Format(time.RFC3339)
	respOK := h.post("d-skew-ok", "push", body, func(r *http.Request) {
		r.Header.Set("X-Sauron-Timestamp", fresh)
	})
	if respOK.StatusCode != http.StatusAccepted {
		t.Fatalf("fresh timestamp must 202, got %d", respOK.StatusCode)
	}
}

func TestHookSizeCap413(t *testing.T) {
	h := newHarness(t, nil)
	big := make([]byte, 2048)
	for i := range big {
		big[i] = 'a'
	}
	resp := h.post("d-big", "push", big, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized payload must 413, got %d", resp.StatusCode)
	}
	if _, err := h.st.GetDelivery(context.Background(), "github", "d-big"); err == nil {
		t.Fatalf("oversized payload must not be persisted")
	}
}
