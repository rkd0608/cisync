package server

import (
	"net/http"
	"strings"
	"testing"
)

/**
 * P0-1 / I-04 regression matrix: every mutating job endpoint requires an
 * Authorization: Bearer <job-lease-token> credential whose signature,
 * audience, expiry and run/attempt/fence claims bind to the request.
 * Failures are typed 401 {"error":{"code":"unauthorized"}} — never 4xx/5xx
 * shapes that leak which check failed.
 */

func TestLeaseMissingHeaderUnauthorized(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-noauth"})
	h.claimWithToken("w-1", 1)

	respNoHeader := h.rawComplete("run-noauth", 1, "succeeded", "")
	if respNoHeader.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing Authorization header must 401, got %d", respNoHeader.StatusCode)
	}
	assertUnauthorizedBody(t, respNoHeader)
}

func TestLeaseExpiredTokenUnauthorized(t *testing.T) {
	h := newHarness(t)
	token := h.enqueueToken(testJobParams{runID: "run-exp"})
	h.claimWithToken("w-1", 1)

	expired := h.mintExpired(token)
	resp := h.rawComplete("run-exp", 1, "succeeded", expired)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired lease must 401, got %d", resp.StatusCode)
	}
	assertUnauthorizedBody(t, resp)
}

func TestLeaseWrongAudienceAndTamperedClaimsUnauthorized(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-bad"})
	token := h.tokenFor("run-bad")
	h.claimWithToken("w-1", 1)

	if resp := h.rawComplete("run-bad", 1, "succeeded", h.mintForeignAudience(token)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-audience lease must 401, got %d", resp.StatusCode)
	}
	if resp := h.rawComplete("run-bad", 1, "succeeded", tamperSegment(token)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered lease must 401, got %d", resp.StatusCode)
	}
	if resp := h.rawComplete("run-bad", 1, "succeeded", h.mintRogue(token)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("foreign-key lease must 401, got %d", resp.StatusCode)
	}
}

func TestLeaseClaimPathBindingEnforced(t *testing.T) {
	h := newHarness(t)
	jobA := h.enqueue(testJobParams{runID: "run-a"})
	jobB := h.enqueue(testJobParams{runID: "run-b"})
	tokenA, tokenB := jobA.LeaseToken, jobB.LeaseToken
	h.claimWithToken("w-1", 2)

	// A's credential presented against B's job ⇒ unauthorized (I-04 scoping).
	if resp, _ := h.completeWithToken("run-b", 1, "succeeded", tokenA); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-run credential reuse must 401, got %d", resp.StatusCode)
	}
	// Epoch currency stays with the fenced store write (I-11): a stale
	// epoch under a VALID credential is 409 fence_mismatch, never 401.
	if resp, body := h.completeWithToken("run-a", 0, "succeeded", tokenA); resp.StatusCode != http.StatusConflict || body["reason"] != "fence_mismatch" {
		t.Fatalf("stale epoch under valid lease must 409 fence_mismatch, got %d %v", resp.StatusCode, body)
	}
	// Matching credential passes both gates end to end.
	if resp, body := h.completeWithToken("run-a", 1, "succeeded", tokenA); resp.StatusCode != http.StatusOK || body["accepted"] != true {
		t.Fatalf("valid lease must complete: %d %v", resp.StatusCode, body)
	}
	if resp, _ := h.heartbeatWithToken("run-b", 1, tokenB); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid lease heartbeat must 204, got %d", resp.StatusCode)
	}
	if resp, _ := h.cancelWithToken("run-b", "superseded", tokenB); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid lease cancel must 204, got %d", resp.StatusCode)
	}
}

func TestLeaseClaimResponseCarriesCredential(t *testing.T) {
	h := newHarness(t)
	token := h.enqueue(testJobParams{runID: "run-handoff"}).LeaseToken
	claimed := h.claimWithToken("w-1", 1)
	if len(claimed) != 1 || claimed[0].LeaseToken != token {
		t.Fatalf("claim response must hand the stored credential to the worker: %+v", claimed)
	}
}

func assertUnauthorizedBody(t *testing.T, resp *http.Response) {
	t.Helper()
	body := decodeBody(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("401 must carry typed error envelope, got %v", body)
	}
	if errObj["code"] != "unauthorized" {
		t.Fatalf("error.code must be unauthorized, got %v", errObj["code"])
	}
}

// tamperSegment flips one character INSIDE the payload segment: changing the
// signed bytes guarantees either a claims change or a signature mismatch.
func tamperSegment(token string) string {
	parts := strings.Split(token, ".")
	raw := []byte(parts[1])
	for i := range raw {
		if raw[i] == 'A' {
			raw[i] = 'B'
			parts[1] = string(raw)
			return strings.Join(parts, ".")
		}
	}
	parts[1] = "eyJ" + parts[1]
	return strings.Join(parts, ".")
}
