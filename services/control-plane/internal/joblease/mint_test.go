package joblease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The control-plane mints one lease per dispatched run (B2); these checks
// pin the mint-side guarantees the fleet verifier relies on.

func TestMintProducesThreeSegmentCompactJWT(t *testing.T) {
	signer, err := NewSignerForTesting()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	now := time.Now()
	token, err := signer.Mint(Claims{
		Audience:   Audience,
		ID:         JTIBuilds("run_x", 2, 7),
		RunID:      "run_x",
		Attempt:    2,
		FenceToken: 7,
		Repo:       "acme/payments",
		Tier:       1,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(LeaseTTLMax).Unix(),
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got := strings.Count(token, "."); got != 2 {
		t.Fatalf("compact JWT must have three segments, got %d: %q", got, token)
	}
}

func TestMintRejectsJtiClaimMismatch(t *testing.T) {
	signer, _ := mustSigner(t)
	claims := Claims{
		Audience:   Audience,
		ID:         "fleet:other:1:1",
		RunID:      "run_a",
		Attempt:    1,
		FenceToken: 1,
		IssuedAt:   time.Now().Unix(),
		ExpiresAt:  time.Now().Add(time.Minute).Unix(),
	}
	if _, err := signer.Mint(claims); err == nil {
		t.Fatal("jti not matching run/attempt/fence must fail at mint-time")
	}
}

func TestSignerLoadsFromPKCS8PEMFile(t *testing.T) {
	signer, err := NewSignerForTesting()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "joblease_ed25519.dev.key")
	if err := os.WriteFile(path, signer.PrivatePEM(), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	reloaded, err := NewSignerFromPEMFile(path)
	if err != nil {
		t.Fatalf("load from pem file: %v", err)
	}
	token, err := reloaded.Mint(Claims{
		Audience:   Audience,
		ID:         JTIBuilds("run_pem", 1, 3),
		RunID:      "run_pem",
		Attempt:    1,
		FenceToken: 3,
		Repo:       "acme/payments",
		Tier:       2,
		IssuedAt:   time.Now().Unix(),
		ExpiresAt:  time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("mint after reload: %v", err)
	}
	publicVerifier, err := NewVerifierFromPublicPEM(signer.PublicPEM())
	if err != nil {
		t.Fatalf("public verifier: %v", err)
	}
	if _, err := publicVerifier.Verify(token); err != nil {
		t.Fatalf("reloaded signer must produce tokens the public key accepts: %v", err)
	}
}

func TestNewSignerFromPEMFileRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "junk.key")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if _, err := NewSignerFromPEMFile(path); err == nil {
		t.Fatal("garbage key file must be rejected")
	}
	if _, err := NewSignerFromPEMFile(filepath.Join(dir, "missing.key")); err == nil {
		t.Fatal("missing key file must be rejected")
	}
}
