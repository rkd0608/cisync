package api

import (
	"testing"
	"time"
)

func TestVerifyGitHubSignature(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"action":"opened"}`)
	sig := signBody(secret, body)

	if !VerifyGitHubSignature(secret, body, sig) {
		t.Fatalf("valid signature rejected")
	}
	if VerifyGitHubSignature([]byte("wrong"), body, sig) {
		t.Fatalf("wrong secret accepted")
	}
	if VerifyGitHubSignature(secret, []byte(`{"action":"closed"}`), sig) {
		t.Fatalf("tampered body accepted")
	}
	if VerifyGitHubSignature(secret, body, "sha256=deadbeef") {
		t.Fatalf("garbage hex accepted")
	}
	if VerifyGitHubSignature(secret, body, "md5=abc") {
		t.Fatalf("non-sha256 prefix accepted")
	}
	if VerifyGitHubSignature(secret, body, "") {
		t.Fatalf("empty header accepted")
	}
}

func TestVerifyTimestampSkew(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	tol := 5 * time.Minute

	if err := VerifyTimestampSkew("", now, tol); err != nil {
		t.Fatalf("absent header must pass: %v", err)
	}
	if err := VerifyTimestampSkew(now.Add(-4*time.Minute).Format(time.RFC3339), now, tol); err != nil {
		t.Fatalf("4m old must pass: %v", err)
	}
	if err := VerifyTimestampSkew(now.Add(4*time.Minute).Format(time.RFC3339), now, tol); err != nil {
		t.Fatalf("4m future must pass: %v", err)
	}
	if err := VerifyTimestampSkew(now.Add(-6*time.Minute).Format(time.RFC3339), now, tol); err == nil {
		t.Fatalf("-6m skew must be rejected")
	}
	if err := VerifyTimestampSkew(now.Add(6*time.Minute).Format(time.RFC3339), now, tol); err == nil {
		t.Fatalf("+6m skew must be rejected")
	}
	if err := VerifyTimestampSkew("not-a-time", now, tol); err == nil {
		t.Fatalf("malformed header must be rejected")
	}
	millis := now.Add(-time.Minute).UnixMilli()
	if err := VerifyTimestampSkew(int64String(millis), now, tol); err != nil {
		t.Fatalf("unix millis form must pass: %v", err)
	}
}
