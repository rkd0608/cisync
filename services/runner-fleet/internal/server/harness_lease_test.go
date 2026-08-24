package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/joblease"
)

func (h *harness) rawComplete(runID string, fence int64, status, token string) *http.Response {
	resp, _ := h.postWithAuth("/internal/fleet/jobs/"+runID+"/complete", token, map[string]any{
		"fence_token":            fence,
		"status":                 status,
		"logs_digest":            digestFor([]byte("logs-" + runID)),
		"artifact_digests":       []string{digestFor([]byte("art-" + runID))},
		"duration_ms":            42000,
		"actual_cost_millicents": 180,
	})
	return resp
}

func (h *harness) completeWithToken(runID string, fence int64, status, token string) (*http.Response, map[string]any) {
	return h.postWithAuth("/internal/fleet/jobs/"+runID+"/complete", token, map[string]any{
		"fence_token":            fence,
		"status":                 status,
		"logs_digest":            digestFor([]byte("logs-" + runID)),
		"artifact_digests":       []string{digestFor([]byte("art-" + runID))},
		"duration_ms":            42000,
		"actual_cost_millicents": 180,
	})
}

// completeCensus posts a completion carrying an outcome census object.
func (h *harness) completeCensus(runID string, fence int64, token string, results map[string]any) (*http.Response, map[string]any) {
	return h.postWithAuth("/internal/fleet/jobs/"+runID+"/complete", token, map[string]any{
		"fence_token":            fence,
		"status":                 "succeeded",
		"logs_digest":            digestFor([]byte("logs-" + runID)),
		"artifact_digests":       []string{digestFor([]byte("art-" + runID))},
		"duration_ms":            42000,
		"actual_cost_millicents": 180,
		"results":                results,
	})
}

func (h *harness) heartbeatWithToken(runID string, fence int64, token string) (*http.Response, map[string]any) {
	return h.postWithAuth("/internal/fleet/jobs/"+runID+"/heartbeat", token, map[string]any{"fence_token": fence})
}

func (h *harness) cancelWithToken(runID, reason, token string) (*http.Response, map[string]any) {
	return h.postWithAuth("/internal/fleet/jobs/"+runID+"/cancel", token, map[string]any{"reason": reason})
}

// mintExpired/mintForeignAudience/mintRogue produce structurally-valid tokens
// that must each fail exactly one verification dimension.
func (h *harness) mintExpired(storedToken string) string {
	parts := decodeJWT(storedToken)
	claims := h.claimsOf(parts)
	claims.ExpiresAt = h.clock().Add(-time.Minute).Unix()
	reSigned, err := h.signer.Mint(claims)
	if err != nil {
		h.t.Fatalf("mint expired: %v", err)
	}
	return reSigned
}

func (h *harness) mintForeignAudience(storedToken string) string {
	parts := decodeJWT(storedToken)
	claims := h.claimsOf(parts)
	claims.Audience = "other-service"
	token, err := h.signer.Mint(claims)
	if err == nil {
		return token
	}
	// Signer refuses foreign audiences at mint-time (fail-closed), so forge
	// the payload bytes directly and leave the signature stale instead.
	return parts[0] + "." + b64JSON(claims) + "." + parts[2]
}

func (h *harness) mintRogue(storedToken string) string {
	parts := decodeJWT(storedToken)
	claims := h.claimsOf(parts)
	token, err := h.rogue.Mint(claims)
	if err != nil {
		h.t.Fatalf("mint rogue: %v", err)
	}
	return token
}

type jwtParts [3]string

func decodeJWT(token string) jwtParts {
	split := strings.Split(token, ".")
	var p jwtParts
	for i := 0; i < len(split) && i < 3; i++ {
		p[i] = split[i]
	}
	return p
}

func (h *harness) claimsOf(parts jwtParts) joblease.Claims {
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		h.t.Fatalf("decode payload: %v", err)
	}
	var claims joblease.Claims
	if err := json.Unmarshal(raw, &claims); err != nil {
		h.t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

func b64JSON(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (h *harness) postWithAuth(path, token string, body any) (*http.Response, map[string]any) {
	h.t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, h.svc.URL+path, bytes.NewReader(raw))
	if err != nil {
		h.t.Fatalf("request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	_ = json.Unmarshal(rawBody, &decoded)
	resp.Body = io.NopCloser(bytes.NewReader(rawBody))
	return resp, decoded
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(rawBody))
	var decoded map[string]any
	_ = json.Unmarshal(rawBody, &decoded)
	return decoded
}
