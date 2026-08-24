// Package joblease implements the Ed25519-signed job-lease tokens required
// by THREAT_MODEL B2 / I-04: control-plane MINTS one per dispatched run;
// runner-fleet VERIFIES signature, audience, expiry and claim-vs-request
// binding before accepting heartbeat/complete/cancel. The fleet side is
// verify-ONLY by construction — no private-key loader exists here (B6 key
// custody: control-plane only). Format is compact JWT with EdDSA
// (RFC 8032) signatures, base64url segments, zero external dependencies.
package joblease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// LeaseTTLMax is the binding ceiling for job-lease lifetime (B2: exp ≤ 60 m).
const LeaseTTLMax = time.Hour

// Audience is the only accepted aud claim value.
const Audience = "sauron-fleet"

// ErrInvalid is the single failure shape returned by Verify; callers map it
// to the typed unauthorized error, never leaking the internal cause.
var ErrInvalid = errors.New("joblease: invalid token")

// Claims are the lease identity fields bound into every token. jti format:
// "fleet:<run_id>:<attempt>:<fence_token>" so I-03's one-record-per-jti rule
// keys on the same string the evidence ledger stores.
type Claims struct {
	Audience   string `json:"aud"`
	ID         string `json:"jti"`
	RunID      string `json:"run_id"`
	Attempt    int    `json:"attempt"`
	FenceToken int64  `json:"fence_token"`
	Repo       string `json:"repo"`
	Tier       int    `json:"tier"`
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

// JTIBuilds renders the canonical jti for a run/attempt/fence triple.
func JTIBuilds(runID string, attempt int, fence int64) string {
	return fmt.Sprintf("fleet:%s:%d:%d", runID, attempt, fence)
}

// Signer mints lease tokens; it lives ONLY in control-plane deployments.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSignerForTesting generates an in-memory keypair for test harnesses and
// exposes the matching public PEM. Production wiring loads a PEM file via
// control-plane config instead.
func NewSignerForTesting() (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("joblease: generate key: %w", err)
	}
	return &Signer{priv: priv}, nil
}

// NewSigner parses a PKCS#8 Ed25519 private key PEM ("-----BEGIN PRIVATE
// KEY-----", openssl genpkey output — same pattern as the ledger signer).
func NewSigner(privatePEM []byte) (*Signer, error) {
	block, _ := pem.Decode(privatePEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("joblease: no PKCS#8 PRIVATE KEY block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("joblease: parse pkcs8: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("joblease: key is %T, want ed25519.PrivateKey", parsed)
	}
	return &Signer{priv: priv}, nil
}

// PublicPEM returns the SPKI public key PEM for distribution to verifiers.
func (s *Signer) PublicPEM() []byte {
	pub, ok := s.priv.Public().(ed25519.PublicKey)
	if !ok {
		panic("joblease: ed25519 private key without public half")
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		panic(fmt.Sprintf("joblease: marshal public key: %v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// Mint signs the claims; TTL beyond LeaseTTLMax fails closed at mint-time so
// an over-long lease can never exist regardless of caller discipline.
func (s *Signer) Mint(claims Claims) (string, error) {
	if claims.ExpiresAt-claims.IssuedAt > int64(LeaseTTLMax/time.Second) {
		return "", fmt.Errorf("joblease: ttl %ds exceeds cap %s",
			claims.ExpiresAt-claims.IssuedAt, LeaseTTLMax)
	}
	if claims.Audience != Audience {
		return "", fmt.Errorf("joblease: audience must be %q", Audience)
	}
	if want := JTIBuilds(claims.RunID, claims.Attempt, claims.FenceToken); claims.ID != want {
		return "", fmt.Errorf("joblease: jti %q must equal %q", claims.ID, want)
	}
	return signCompact(s.priv, claims)
}
