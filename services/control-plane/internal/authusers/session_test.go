package authusers

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func testSigner(t *testing.T) *Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return NewSignerFromKey(priv)
}

func fixedNow() time.Time { return time.Unix(1_756_200_000, 0).UTC() }

func TestSessionSignVerifyRoundtrip(t *testing.T) {
	sg := testSigner(t)
	tok, err := sg.Mint(SessionClaims{Email: "dev@example.com", UID: "01JTEST"}, fixedNow())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, err := sg.Verifier().Verify(tok, fixedNow())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Email != "dev@example.com" || got.UID != "01JTEST" {
		t.Fatalf("claims lost: %+v", got)
	}
	if want := fixedNow().Add(SessionTTL).Unix(); got.Exp != want {
		t.Fatalf("exp=%d want %d (30d after iat)", got.Exp, want)
	}
	if got.Iat != fixedNow().Unix() {
		t.Fatalf("iat drifted from mint instant")
	}
}

func TestSessionTamperRejected(t *testing.T) {
	sg := testSigner(t)
	v := sg.Verifier()
	tok, err := sg.Mint(SessionClaims{Email: "real@example.com", UID: "u1"}, fixedNow())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	parts := strings.Split(tok, ".")
	forgedPayload := base64URL([]byte(`{"sub":"attacker@evil.com","uid":"u9"}`))
	cases := map[string]string{
		"swapped_payload": forgedPayload + "." + parts[1] + "." + parts[2],
		"truncated_sig":   parts[0] + "." + parts[1] + ".AAAA",
		"garbage":         "not.a.jwt",
		"empty":           "",
	}
	for name, bad := range cases {
		if _, err := v.Verify(bad, fixedNow()); err == nil {
			t.Errorf("%s: tampered token verified; must fail closed", name)
		}
	}
}

func TestSessionExpiryRejected(t *testing.T) {
	sg := testSigner(t)
	v := sg.Verifier()
	tok, _ := sg.Mint(SessionClaims{Email: "x@y.z"}, fixedNow())
	afterTTL := fixedNow().Add(SessionTTL + time.Second)
	if _, err := v.Verify(tok, afterTTL); err == nil {
		t.Fatal("expired token verified")
	}
	// Boundary: one second BEFORE expiry still valid.
	beforeTTL := fixedNow().Add(SessionTTL - time.Second)
	if _, err := v.Verify(tok, beforeTTL); err != nil {
		t.Fatalf("token inside TTL rejected: %v", err)
	}
}

func TestSessionWrongIssuerKeyRejected(t *testing.T) {
	minter := testSigner(t)
	verifier := testSigner(t).Verifier()
	tok, _ := minter.Mint(SessionClaims{Email: "x@y.z"}, fixedNow())
	if _, err := verifier.Verify(tok, fixedNow()); err == nil {
		t.Fatal("cross-key token verified")
	}
}

func TestSignerFromPEMFileRoundtrip(t *testing.T) {
	sg := testSigner(t)
	dir := t.TempDir()
	privPath := filepath.Join(dir, "session_ed25519.key")
	pubPath := filepath.Join(dir, "session_ed25519.pub")
	if err := os.WriteFile(privPath, sg.PrivatePEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, sg.PublicPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewSignerFromPEMFile(privPath)
	if err != nil {
		t.Fatalf("load priv pem: %v", err)
	}
	tok, err := reloaded.Mint(SessionClaims{Email: "k@x.y"}, fixedNow())
	if err != nil {
		t.Fatalf("mint post-reload: %v", err)
	}
	if _, err := sg.Verifier().Verify(tok, fixedNow()); err != nil {
		t.Fatalf("reloaded signer minted under different key: %v", err)
	}
	if err := os.WriteFile(pubPath+".broken", []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerifierFromPublicPEMFile(pubPath + ".broken"); err == nil {
		t.Fatal("invalid public PEM loaded without error")
	}
}
