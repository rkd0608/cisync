// Package joblease implements the control-plane side of the Ed25519-signed
// job-lease tokens (THREAT_MODEL B2 / I-04): one token minted per dispatched
// run, claims bound to run_id/attempt/fence/repo/tier with exp ≤ 60 m. The
// runner-fleet module carries the verify-only twin of this package — private
// keys never leave control-plane custody (B6).
package joblease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// LeaseTTLMax is the binding ceiling for job-lease lifetime (B2: exp ≤ 60 m).
const LeaseTTLMax = time.Hour

// Audience is the only accepted aud claim value.
const Audience = "sauron-fleet"

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

// ErrInvalid is the single failure shape returned by Verify; callers map it
// to the typed unauthorized error, never leaking the internal cause.
var ErrInvalid = errors.New("joblease: invalid token")

// JTIBuilds renders the canonical jti for a run/attempt/fence triple.
func JTIBuilds(runID string, attempt int, fence int64) string {
	return fmt.Sprintf("fleet:%s:%d:%d", runID, attempt, fence)
}

// Signer mints lease tokens; instantiated once at startup from
// SAURON_CTRL_JOBLEASE_KEY_FILE.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSignerForTesting generates an in-memory keypair for test harnesses.
func NewSignerForTesting() (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("joblease: generate key: %w", err)
	}
	return &Signer{priv: priv}, nil
}

// NewSignerFromPEMFile loads a PKCS#8 Ed25519 private key PEM ("-----
// BEGIN PRIVATE KEY-----", openssl genpkey output — same pattern as the
// ledger checkpoint signer, but a DEDICATED key: compromise of either key
// must not cascade into the other trust domain).
func NewSignerFromPEMFile(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("joblease: read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("joblease: %s has no PKCS#8 PRIVATE KEY block", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("joblease: parse pkcs8 in %s: %w", path, err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("joblease: %s holds %T, want ed25519.PrivateKey", path, parsed)
	}
	return &Signer{priv: priv}, nil
}

// PublicPEM returns the SPKI public key PEM for distribution to verifiers
// (runner-fleet mounts it via SAURON_FLEET_JOBLEASYPUB_KEY_FILE).
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

// PrivatePEM renders the signing key as PKCS#8 PEM (dev key generation and
// round-trip tests only; production keeps the file on disk untouched).
func (s *Signer) PrivatePEM() []byte {
	der, err := x509.MarshalPKCS8PrivateKey(s.priv)
	if err != nil {
		panic(fmt.Sprintf("joblease: marshal private key: %v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Mint signs the claims; TTL beyond LeaseTTLMax or a jti not derived from
// run/attempt/fence fails closed at mint-time so malformed leases can never
// exist regardless of caller discipline.
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
