package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cisync.dev/cisync/ingest/internal/domain"
	"cisync.dev/cisync/ingest/internal/forward"
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
	// B7 seam: each of the FOUR triggering requests must produce exactly
	// ONE signed audit marker to control-plane — never a delivery forward.
	markers := waitForEnvelopes(t, h, 4)
	for _, env := range markers {
		if env.QuarantineReason != "signature_verification_failed" || env.EventKind != "sig_failed" {
			t.Fatalf("only sig-failed markers may reach ctrl after rejections, got %+v", env)
		}
		if !strings.HasSuffix(env.ExtDeliveryID, ".sigfailed") &&
			!strings.Contains(env.ExtDeliveryID, ".sigfailed.") {
			t.Fatalf("marker id %q must be nonce-suffixed under the original GUID", env.ExtDeliveryID)
		}
	}
	assertNoEnvelopes(t, h)

	// A later VALID redelivery of a rejected GUID must not be treated as a
	// duplicate — quarantine rows never occupy the dedup slot — and must
	// forward as a NORMAL envelope (no flags).
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
	envs := waitForEnvelopes(t, h, 1)
	if envs[0].QuarantineReason != "" || envs[0].DuplicateSuspect {
		t.Fatalf("valid redelivery must forward unflagged, got %+v", envs[0])
	}
}

// waitForEnvelopes polls the harness channel until n envelopes arrive or a
// deadline passes (marker sending is asynchronous by design).
func waitForEnvelopes(t *testing.T, h *harness, n int) []forward.Envelope {
	t.Helper()
	var out []forward.Envelope
	deadline := time.Now().Add(2 * time.Second)
	for len(out) < n && time.Now().Before(deadline) {
		select {
		case env := <-h.ctrlCalls:
			out = append(out, env)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if len(out) != n {
		t.Fatalf("received %d envelopes, want %d", len(out), n)
	}
	return out
}

// assertNoEnvelopes fails when any envelope arrives within a short grace
// window.
func assertNoEnvelopes(t *testing.T, h *harness) {
	t.Helper()
	select {
	case env := <-h.ctrlCalls:
		t.Fatalf("unexpected envelope reached ctrl: %+v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHookTimestampSkewRejected(t *testing.T) {
	h := newHarness(t, nil)
	body := validPayload()

	old := h.now.Add(-6 * time.Minute).Format(time.RFC3339)
	resp := h.post("d-skew-old", "push", body, func(r *http.Request) {
		r.Header.Set("X-CISync-Timestamp", old)
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale timestamp must 401, got %d", resp.StatusCode)
	}

	fresh := h.now.Format(time.RFC3339)
	respOK := h.post("d-skew-ok", "push", body, func(r *http.Request) {
		r.Header.Set("X-CISync-Timestamp", fresh)
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
