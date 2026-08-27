package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cisync.dev/cisync/control-plane/internal/store"
)

// writeTempSessionKey materializes an in-memory Ed25519 key as the PKCS#8
// PEM file layout prod uses, exercising the exact UseSessionKey load path.
func writeTempSessionKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session_ed25519.key")
	if err := os.WriteFile(path,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// PG-backed auth endpoint regression (signup/login/me). Skips without
// TEST_PG_DSN so hermetic runs stay green. WHY real DB: duplicate-email
// uniqueness, rate-bucket accounting and citext case folding only exist in
// Postgres; the pure crypto layers are covered hermetically elsewhere.

func authPGServer(t *testing.T) *httptest.Server {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping auth pg test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := testConfig()
	srv := NewServer(cfg, st, nil)
	// Session keys come from a PEM file in prod; the test mints in-memory
	// via the same seam UseSessionKey uses after loading.
	if err := srv.UseSessionKey(writeTempSessionKey(t)); err != nil {
		t.Fatalf("session key: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctxDone := context.Background()
		_, _ = st.Pool.Exec(ctxDone,
			`DELETE FROM ctrl.users WHERE email LIKE 'authtest\_%@example.com' ESCAPE '\'`)
		_, _ = st.Pool.Exec(ctxDone, `DELETE FROM ctrl.rate_limits WHERE bucket LIKE 'authlogin:%'`)
		st.Close()
		ts.Close()
	})
	return ts
}

func authPost(t *testing.T, ts *httptest.Server, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		return code
	}
	return ""
}

func TestAuthSignupLoginMeFlow(t *testing.T) {
	ts := authPGServer(t)
	email := fmt.Sprintf("authtest_flow_%d@example.com", os.Getpid())
	pass := "correct-horse-42"

	resp, body := authPost(t, ts, "/v1/auth/signup", `{"email":"`+email+`","password":"`+pass+`"}`)
	if resp.StatusCode != http.StatusCreated || body["user"] == nil {
		t.Fatalf("signup = %d %v, want 201 {user}", resp.StatusCode, body)
	}

	resp, body = authPost(t, ts, "/v1/auth/login", `{"email":"`+strings.ToUpper(email)+`","password":"`+pass+`"}`)
	if resp.StatusCode != http.StatusOK || body["token"] == "" {
		t.Fatalf("login(case-folded email) = %d %v, want 200 {token,user}", resp.StatusCode, body)
	}
	token, _ := body["token"].(string)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	me, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	var meBody struct {
		User struct{ Email string } `json:"user"`
	}
	if err := json.NewDecoder(me.Body).Decode(&meBody); err != nil || meBody.User.Email != strings.ToLower(email) {
		t.Fatalf("me = %s %s %v, want lowercase email echo", me.Status, meBody.User.Email, err)
	}
}

func TestSignupValidationAndConflictMatrix(t *testing.T) {
	ts := authPGServer(t)
	p := os.Getpid()
	cases := []struct {
		name, email, pass, wantCode string
		wantStatus                  int
	}{
		{"short_password", fmt.Sprintf("authtest_short_%d@example.com", p), "only8char", "weak_password", 400},
		{"bad_email", "not-an-email", "long-enough-passphrase", "invalid_email", 400},
		{"no_at", "userwithoutdomain.com", "long-enough-passphrase", "invalid_email", 400},
	}
	for _, tc := range cases {
		resp, body := authPost(t, ts, "/v1/auth/signup",
			`{"email":"`+tc.email+`","password":"`+tc.pass+`"}`)
		if resp.StatusCode != tc.wantStatus || errCode(body) != tc.wantCode {
			t.Errorf("%s: (%d,%s), want (%d,%s)", tc.name, resp.StatusCode, errCode(body), tc.wantStatus, tc.wantCode)
		}
	}

	first := fmt.Sprintf("authtest_dup_%d@example.com", p)
	if resp, _ := authPost(t, ts, "/v1/auth/signup",
		`{"email":"`+first+`","password":"long-enough-passphrase"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first signup = %d, want 201", resp.StatusCode)
	}
	// Duplicate must conflict even under different case (citext UNIQUE).
	resp, body := authPost(t, ts, "/v1/auth/signup",
		`{"email":"`+strings.ToUpper(first)+`","password":"a-different-password!"}`)
	if resp.StatusCode != http.StatusConflict || errCode(body) != "exists" {
		t.Fatalf("duplicate(case-folded) = (%d,%s), want 409 exists", resp.StatusCode, errCode(body))
	}
}

func TestLoginUniformFailureAndRateLimit(t *testing.T) {
	ts := authPGServer(t)
	p := os.Getpid()
	registered := fmt.Sprintf("authtest_rl_%d@example.com", p)
	if resp, _ := authPost(t, ts, "/v1/auth/signup",
		`{"email":"`+registered+`","password":"the-right-password"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed signup = %d", resp.StatusCode)
	}

	for i := 0; i < loginRateCapacity; i++ {
		resp, body := authPost(t, ts, "/v1/auth/login",
			`{"email":"`+registered+`","password":"wrong-password-999"}`)
		if resp.StatusCode != http.StatusUnauthorized || errCode(body) != "invalid_credentials" {
			t.Fatalf("attempt %d: (%d,%s), want uniform 401 invalid_credentials", i+1, resp.StatusCode, errCode(body))
		}
		msg, _ := body["error"].(map[string]any)["message"].(string)
		if msg == "" || strings.Contains(strings.ToLower(msg), registered) {
			t.Fatalf("uniform message must never echo identity: %q", msg)
		}
	}
	// Bucket drained → next attempt is 429 regardless of correctness.
	resp, body := authPost(t, ts, "/v1/auth/login",
		`{"email":"`+registered+`","password":"the-right-password"}`)
	if resp.StatusCode != http.StatusTooManyRequests || errCode(body) != "rate_limited" {
		t.Fatalf("over-cap correct login = (%d,%s), want 429 rate_limited", resp.StatusCode, errCode(body))
	}
}
