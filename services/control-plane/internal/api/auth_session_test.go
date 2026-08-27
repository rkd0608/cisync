package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cisync.dev/cisync/control-plane/internal/authusers"
)

func timeNowUTC() time.Time { return time.Now().UTC() }

// authServerForTest builds a server whose session keys live in memory (no
// PEM file) so /v1/auth/me contract tests stay hermetic.
func authServerForTest(t *testing.T) (*httptest.Server, *authusers.Signer) {
	t.Helper()
	cfg := testConfig()
	srv := NewServer(cfg, nil, nil)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	signer := authusers.NewSignerFromKey(priv)
	srv.sessionSigner = signer
	srv.sessionVerifier = signer.Verifier()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

func getMe(ts *httptest.Server, token string) (*http.Response, ErrorEnvelope) {
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/auth/me", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, ErrorEnvelope{}
	}
	defer resp.Body.Close()
	var env ErrorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp, env
}

func TestAuthMeHappyPath(t *testing.T) {
	ts, signer := authServerForTest(t)
	token, err := signer.Mint(authusers.SessionClaims{Email: "dev@example.com", UID: "01J"}, timeNowUTC())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	resp, _ := getMe(ts, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d, want 200", resp.StatusCode)
	}
}

func TestAuthMeTamperedTokenUniform401(t *testing.T) {
	ts, signer := authServerForTest(t)
	good, _ := signer.Mint(authusers.SessionClaims{Email: "dev@example.com"}, timeNowUTC())
	parts := strings.SplitN(good, ".", 3)
	forgeries := map[string]string{
		"tampered_payload": parts[0] + ".eyJzdWIiOiJldmlsQGV2aWwuY29tIn0." + parts[2],
		"garbage":          "abc.def.ghi",
		"empty":            "",
	}
	for name, tok := range forgeries {
		resp, env := getMe(ts, tok)
		if resp.StatusCode != http.StatusUnauthorized || env.Error.Code != "unauthorized" {
			t.Errorf("%s: got (%d,%s), want uniform 401 unauthorized", name, resp.StatusCode, env.Error.Code)
		}
	}
	// Wrong-issuer token must also bounce uniformly.
	otherKey := authusers.NewSignerFromKey(mustOtherPriv(t))
	foreign, _ := otherKey.Mint(authusers.SessionClaims{Email: "x@y.z"}, timeNowUTC())
	resp, _ := getMe(ts, foreign)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("cross-key token = %d, want 401", resp.StatusCode)
	}
}

func mustOtherPriv(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestAuthEndpointsFailClosedWithoutKeyOrStore(t *testing.T) {
	cfg := testConfig()
	cfg.SessionKeyFile = ""
	srv := NewServer(cfg, nil, nil) // no store, no session key
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"email":"a@b.co","password":"long-enough-pass"}`
	cases := []struct{ method, path string }{
		{http.MethodPost, "/v1/auth/signup"},
		{http.MethodPost, "/v1/auth/login"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		defer resp.Body.Close()
		var env ErrorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&env)
		if resp.StatusCode != http.StatusServiceUnavailable || env.Error.Code != "unavailable" {
			t.Errorf("%s %s without store/key: (%d,%s)", tc.method, tc.path, resp.StatusCode, env.Error.Code)
		}
	}
}

func TestSignupValidationMatrixNoDBNeededForShape(t *testing.T) {
	// With no store the request dies at 503 AFTER validation; to pin the
	// validation codes themselves we assert the pure helpers directly —
	// cheap and exhaustive, DB matrix lives in auth_users_pg_test.go.
	if validEmail("user@sub.example.com") != true ||
		validEmail("no-at-sign") != false ||
		validEmail("@nodomain.com") != false ||
		validEmail("trailing@dot.") != false ||
		validEmail("two@at@signs.com") != false ||
		validEmail("space space@example.com") != false ||
		validEmail(strings.Repeat("l", 65)+"@example.com") != false {
		t.Fatal("email shape validator disagrees with its contract")
	}
	if _, err := authusers.HashPassword("short"); err == nil {
		t.Fatal("short password accepted by policy layer")
	}
}
