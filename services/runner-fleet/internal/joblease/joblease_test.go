package joblease

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Test vectors are produced by the control-plane signer shape (EdDSA JWT,
// base64url segments); the fleet side must be verify-ONLY (B6: private key
// custody stays with control-plane).

func mustSigner(t *testing.T) (*Signer, *Verifier) {
	t.Helper()
	signer, err := NewSignerForTesting()
	if err != nil {
		t.Fatalf("test signer: %v", err)
	}
	verifier, err := NewVerifierFromPublicPEM(signer.PublicPEM())
	if err != nil {
		t.Fatalf("verifier from public pem: %v", err)
	}
	return signer, verifier
}

func validClaims() Claims {
	now := time.Now()
	return Claims{
		Audience:   "cisync-fleet",
		ID:         "fleet:run_abc:1:1",
		RunID:      "run_abc",
		Attempt:    1,
		FenceToken: 1,
		Repo:       "acme/payments",
		Tier:       2,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(30 * time.Minute).Unix(),
	}
}

func TestVerifierAcceptsWellFormedToken(t *testing.T) {
	signer, verifier := mustSigner(t)
	token, err := signer.Mint(validClaims())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.RunID != "run_abc" || got.Attempt != 1 || got.FenceToken != 1 || got.Repo != "acme/payments" || got.Tier != 2 {
		t.Fatalf("claims mismatch: %+v", got)
	}
	if got.ID != "fleet:run_abc:1:1" {
		t.Fatalf("jti mismatch: %q", got.ID)
	}
}

func TestVerifierRejectsExpiredToken(t *testing.T) {
	signer, verifier := mustSigner(t)
	claims := validClaims()
	claims.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	token, err := signer.Mint(claims)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := verifier.Verify(token); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestVerifierRejectsWrongAudience(t *testing.T) {
	signer, verifier := mustSigner(t)
	claims := validClaims()
	claims.Audience = "other-service"
	token, _ := signer.Mint(claims)
	if _, err := verifier.Verify(token); err == nil {
		t.Fatal("wrong audience must be rejected")
	}
}

func TestVerifierRejectsTamperedPayload(t *testing.T) {
	signer, verifier := mustSigner(t)
	claims := validClaims()
	token, _ := signer.Mint(claims)
	parts := strings.Split(token, ".")
	// Tamper INSIDE the decoded payload: whether the base64 text happens to
	// contain a given character is data-dependent (this test was flaky when
	// it scanned for 'A'), so mutate the decoded JSON bytes instead — signed
	// bytes change ⇒ rejection regardless of canonical-base64 trailing bits.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload[0] == '"' {
		payload[0] = '!'
	} else {
		payload[0] = '"'
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	if _, err := verifier.Verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered token must be rejected")
	}
}

func TestVerifierRejectsForeignKeySignature(t *testing.T) {
	_, verifier := mustSigner(t)
	rogue, err := NewSignerForTesting()
	if err != nil {
		t.Fatalf("rogue signer: %v", err)
	}
	token, _ := rogue.Mint(validClaims())
	if _, err := verifier.Verify(token); err == nil {
		t.Fatal("signature by a foreign key must be rejected")
	}
}

func TestVerifierRejectsGarbage(t *testing.T) {
	_, verifier := mustSigner(t)
	for _, bad := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := verifier.Verify(bad); err == nil {
			t.Fatalf("garbage token %q must be rejected", bad)
		}
	}
}

func TestMintEnforcesMaxTTL(t *testing.T) {
	signer, verifier := mustSigner(t)
	claims := validClaims()
	claims.ExpiresAt = claims.IssuedAt + 3600 + 1 // over the 60 m cap
	if _, err := signer.Mint(claims); err == nil {
		t.Fatal("minting beyond the 60-minute cap must fail")
	}
	capped, err := signer.Mint(validClaims())
	if err != nil {
		t.Fatalf("capped mint: %v", err)
	}
	if _, err := verifier.Verify(capped); err != nil {
		t.Fatalf("60m-capped token must verify: %v", err)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	signer, verifier := mustSigner(t)
	token, _ := signer.Mint(validClaims())
	got, ok := FromAuthorizationHeader("Bearer " + token)
	if !ok {
		t.Fatal("bearer header must parse")
	}
	if _, err := verifier.Verify(got); err != nil {
		t.Fatalf("bearer-extracted token must verify: %v", err)
	}
	if _, ok := FromAuthorizationHeader("Basic dXNlcjpwYXNz"); ok {
		t.Fatal("non-bearer scheme must not parse")
	}
}
