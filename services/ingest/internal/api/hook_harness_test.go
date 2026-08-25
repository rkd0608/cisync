package api

import (
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

	"sauron.dev/sauron/ingest/internal/forward"
	"sauron.dev/sauron/ingest/internal/obs"
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
	return newHarnessSecrets(t, ctrlHandler, "test-webhook-secret")
}

// newHarnessSecrets builds the harness with an explicit rotation verify list
// (EC-010); the first entry doubles as the ctrl-hop signing secret.
func newHarnessSecrets(t *testing.T, ctrlHandler http.HandlerFunc, secrets ...string) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		secret:    secrets[0],
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
	secretBytes := make([][]byte, len(secrets))
	for i, s := range secrets {
		secretBytes[i] = []byte(s)
	}
	handler := NewGitHubHookHandler(h.st, fw, metrics, logger, func() time.Time { return h.currentTime() }, 1024, 5*time.Minute, secretBytes)
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
