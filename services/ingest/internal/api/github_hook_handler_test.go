package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
	"sauron.dev/sauron/ingest/internal/forward"
	"sauron.dev/sauron/ingest/internal/obs"
	"sauron.dev/sauron/ingest/internal/retry"
	"sauron.dev/sauron/ingest/internal/store"
)

type harness struct {
	t         *testing.T
	secret    string
	ctrl      *httptest.Server
	svc       *httptest.Server
	st        *store.MemoryStore
	ctrlCalls chan forward.Envelope
	now       time.Time
	mu        sync.Mutex
}

func newHarness(t *testing.T, ctrlHandler http.HandlerFunc) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		secret:    "test-webhook-secret",
		ctrlCalls: make(chan forward.Envelope, 64),
		now:       time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	}
	if ctrlHandler == nil {
		ctrlHandler = func(w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			if sig := r.Header.Get("X-Sauron-Signature"); !verifyCtrlRaw(h.secret, raw, sig) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var env forward.Envelope
			if json.Unmarshal(raw, &env) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if key := r.Header.Get("Idempotency-Key"); key != env.ExtDeliveryID {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			h.ctrlCalls <- env
			w.WriteHeader(http.StatusAccepted)
		}
	}
	h.ctrl = httptest.NewServer(ctrlHandler)
	h.st = store.NewMemoryStore(func() time.Time { return h.currentTime() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fw := forward.New(h.ctrl.URL, []byte(h.secret))
	metrics := obs.New()
	handler := NewGitHubHookHandler(h.st, fw, metrics, logger, func() time.Time { return h.currentTime() }, 1024, 5*time.Minute)
	h.svc = httptest.NewServer(handler)
	t.Cleanup(func() {
		h.ctrl.Close()
		h.svc.Close()
	})
	return h
}

func (h *harness) currentTime() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) post(deliveryID, eventKind string, body []byte, mutate func(*http.Request)) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.svc.URL+"/v1/hooks/github", strings.NewReader(string(body)))
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signBody([]byte(h.secret), body))
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-GitHub-Event", eventKind)
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("post webhook: %v", err)
	}
	resp.Body.Close()
	return resp
}

func validPayload() []byte {
	return []byte(`{"action":"opened","repository":{"full_name":"acme/payments"},"pull_request":{"title":"t","head":{"token":"ghs_dontleakme123456"}}}`)
}

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
		if _, err := h.st.GetDelivery(context.Background(), "github", id); err == nil {
			t.Fatalf("rejected delivery %s must not be persisted for forwarding", id)
		}
	}
	select {
	case <-h.ctrlCalls:
		t.Fatalf("no rejected request may reach control-plane")
	default:
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

func TestRetryWorkerRecoversPendingDeliveries(t *testing.T) {
	var accepted atomicFlag
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if !accepted.get() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		readEnvelope(t, r)
		w.WriteHeader(http.StatusAccepted)
	})

	resp := h.post("retry-1", "push", validPayload(), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("initial attempt must 503, got %d", resp.StatusCode)
	}

	fw := forward.New(h.ctrl.URL, []byte(h.secret))
	metrics := obs.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := retry.NewWorker(h.st, fw, metrics, logger, time.Second, time.Millisecond, 10, func() time.Time { return h.currentTime() })

	h.advance(31 * time.Second)
	worker.ScanOnce(context.Background())
	accepted.set(true)
	h.advance(31 * time.Second)
	worker.ScanOnce(context.Background())

	d, err := h.st.GetDelivery(context.Background(), "github", "retry-1")
	if err != nil {
		t.Fatalf("delivery missing: %v", err)
	}
	if d.Status != domain.StatusForwarded {
		t.Fatalf("worker must eventually forward, status=%s attempts=%d", d.Status, d.Attempts)
	}
}

func TestRetryWorkerMarksExhaustedAsForwardFailed(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h.post("exhaust-1", "push", validPayload(), nil)

	fw := forward.New(h.ctrl.URL, []byte(h.secret))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := retry.NewWorker(h.st, fw, obs.New(), logger, time.Second, time.Millisecond, 3, func() time.Time { return h.currentTime() })

	for i := 0; i < 4; i++ {
		h.advance(31 * time.Second)
		worker.ScanOnce(context.Background())
	}

	d, _ := h.st.GetDelivery(context.Background(), "github", "exhaust-1")
	if d.Status != domain.StatusForwardFailed {
		t.Fatalf("exhausted delivery must be forward_failed, got %s", d.Status)
	}
	if d.Attempts < 3 {
		t.Fatalf("attempts must reach max, got %d", d.Attempts)
	}
}

func readEnvelope(t *testing.T, r *http.Request) forward.Envelope {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read forward body: %v", err)
	}
	r.Body = io.NopCloser(strings.NewReader(string(raw)))
	var env forward.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("invalid envelope json: %v (%s)", err, raw)
	}
	return env
}

func verifyCtrlRaw(secret string, raw []byte, header string) bool {
	const prefix = "sha256="
	if len(header) <= len(prefix) {
		return false
	}
	given, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	return hmac.Equal(given, mac.Sum(nil))
}

type atomicFlag struct {
	mu sync.Mutex
	v  bool
}

func (f *atomicFlag) get() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v
}

func (f *atomicFlag) set(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.v = v
}
