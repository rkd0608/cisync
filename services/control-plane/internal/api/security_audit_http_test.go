package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cisync.dev/cisync/control-plane/internal/audit"
)

// capturingSink records streamed audit events so httptest cases can assert
// exact emission counts without a database.
type capturingSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *capturingSink) record(_ context.Context, ev audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return nil
}

func (c *capturingSink) ofKind(kind audit.Kind) []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, 0, len(c.events))
	for _, ev := range c.events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestSecurityAuditEmissionsTable(t *testing.T) {
	tests := []struct {
		name      string
		kind      audit.Kind
		wantCount int
		do        func(t *testing.T, url string)
	}{
		{
			name:      "missing bearer token emits one authz_rejected",
			kind:      audit.KindAuthzRejected,
			wantCount: 1,
			do: func(t *testing.T, url string) {
				resp, err := http.Post(url+"/v1/change-intents", "application/json", strings.NewReader(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.StatusCode)
				}
			},
		},
		{
			name:      "invalid bearer token emits one authz_rejected",
			kind:      audit.KindAuthzRejected,
			wantCount: 1,
			do: func(t *testing.T, url string) {
				req, _ := http.NewRequest(http.MethodPost, url+"/v1/change-intents", strings.NewReader(`{}`))
				req.Header.Set("Authorization", "Bearer wrong_token")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.StatusCode)
				}
			},
		},
		{
			name:      "bad github webhook signature emits one webhook_signature_failed",
			kind:      audit.KindWebhookSignatureFailed,
			wantCount: 1,
			do: func(t *testing.T, url string) {
				body := []byte(`{"action":"opened"}`)
				mac := hmac.New(sha256.New, []byte("not_the_secret"))
				mac.Write(body)
				req, _ := http.NewRequest(http.MethodPost, url+"/v1/hooks/github", strings.NewReader(string(body)))
				req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				req.Header.Set("X-GitHub-Delivery", "guid-bad-sig-1")
				req.Header.Set("X-GitHub-Event", "pull_request")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", resp.StatusCode)
				}
			},
		},
		{
			name:      "valid-signature marker with no store emits nothing and fails closed",
			kind:      audit.KindWebhookSignatureFailed,
			wantCount: 0,
			do: func(t *testing.T, url string) {
				body := `{"source":"github","ext_delivery_id":"guid-1.sigfailed.01ABC","event_kind":"sig_failed","repo":"a/b","received_at":"2026-01-01T00:00:00Z","payload":{},"quarantine_reason":"signature_verification_failed"}`
				req, _ := http.NewRequest(http.MethodPost, url+"/internal/ctrl/deliveries", strings.NewReader(body))
				req.Header.Set("X-CISync-Signature", "sha256="+signWith(testConfig().WebhookSecret, []byte(body)))
				req.Header.Set("Idempotency-Key", "guid-1.sigfailed.01ABC")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503", resp.StatusCode)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(testConfig(), nil, nil)
			cap := &capturingSink{}
			srv.UseAuditSink(cap.record)
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()

			tc.do(t, ts.URL)

			if !srv.Audit().Stop(auditStopTimeout) {
				t.Fatal("audit stream did not drain")
			}
			got := cap.ofKind(tc.kind)
			if len(got) != tc.wantCount {
				t.Fatalf("%s emissions = %d, want %d", tc.kind, len(got), tc.wantCount)
			}
			if tc.wantCount == 1 && got[0].TenantID != testConfig().TenantID {
				t.Fatalf("tenant_id = %q, want %q", got[0].TenantID, testConfig().TenantID)
			}
		})
	}
}

func signWith(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// decodedAuditDetail is a tiny probe for assertions on event payloads.
func decodedAuditDetail(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	return out
}
