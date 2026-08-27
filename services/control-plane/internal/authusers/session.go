package authusers

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// SessionTTL matches the prior OTP-cookie lifetime (30d) so the login UX
// contract (SPEC §3, cookie Max-Age) is unchanged for operators.
const SessionTTL = 30 * 24 * time.Hour

// SessionClaims is the compact JWT body minted at login. WHY sub=email:
// GET /v1/auth/me must render the account without a DB hit; uid rides along
// so future tenant scoping can key on the stable ULID instead of email.
type SessionClaims struct {
	Email string `json:"sub"` // RFC 7519 subject: the sign-in identity
	UID   string `json:"uid"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

// ErrSessionInvalid is the single opaque failure shape; handlers map it to
// one uniform 401 so tampering, expiry and malformed tokens are
// indistinguishable to clients (no probing oracle).
var ErrSessionInvalid = errors.New("authusers: invalid session token")

// Signer mints session JWTs from the DEDICATED session signing key — never
// the ledger or job-lease key (B2-style trust-domain separation: compromise
// of one key must not cascade into web sessions).
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSignerFromKey wraps an in-memory private key (test harnesses).
func NewSignerFromKey(priv ed25519.PrivateKey) *Signer { return &Signer{priv: priv} }

// NewSignerFromPEMFile loads a PKCS#8 Ed25519 private key PEM, same encoding
// as every other CISync signer. Path comes from CISYNC_SESSION_KEY_FILE.
func NewSignerFromPEMFile(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("authusers: read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("authusers: %s has no PKCS#8 PRIVATE KEY block", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authusers: parse pkcs8 in %s: %w", path, err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("authusers: %s holds %T, want ed25519.PrivateKey", path, parsed)
	}
	return &Signer{priv: priv}, nil
}

// PublicPEM renders the SPKI public half (kept alongside the private file in
// dev-keys for symmetry with the joblease pair; the web tier consumes
// /v1/auth/me over HTTP and never needs this file).
func (s *Signer) PublicPEM() []byte {
	pub := s.priv.Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		panic(fmt.Sprintf("authusers: marshal public key: %v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// PrivatePEM renders the PKCS#8 private PEM (dev-key generation/round-trips;
// production keeps the on-disk file untouched).
func (s *Signer) PrivatePEM() []byte {
	der, err := x509.MarshalPKCS8PrivateKey(s.priv)
	if err != nil {
		panic(fmt.Sprintf("authusers: marshal private key: %v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Mint signs claims with iat=now and exp=iat+SessionTTL. Callers may not pick
// TTLs: session lifetime policy stays pinned to the documented 30 days.
func (s *Signer) Mint(claims SessionClaims, now time.Time) (string, error) {
	if claims.Email == "" {
		return "", fmt.Errorf("authusers: mint requires non-empty email sub")
	}
	claims.Iat = now.Unix()
	claims.Exp = now.Add(SessionTTL).Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("authusers: marshal claims: %w", err)
	}
	header := []byte(`{"alg":"EdDSA","typ":"JWT"}`)
	hB64 := base64.RawURLEncoding.EncodeToString(header)
	pB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(s.priv, []byte(hB64+"."+pB64))
	return hB64 + "." + pB64 + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verifier validates session tokens against the signer's public half.
type Verifier struct {
	pub ed25519.PublicKey
}

// NewVerifierFromPublicPEMFile builds a verify-only validator from an SPKI
// PEM — the twin of the private-file signer (dev key generation + tooling).
func NewVerifierFromPublicPEMFile(path string) (*Verifier, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("authusers: read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("authusers: %s has no PUBLIC KEY block", path)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authusers: parse spki in %s: %w", path, err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("authusers: %s holds %T, want ed25519.PublicKey", path, parsed)
	}
	return &Verifier{pub: pub}, nil
}

// Verifier returns a verifier bound to this signer's public half.
func (s *Signer) Verifier() *Verifier {
	return &Verifier{pub: s.priv.Public().(ed25519.PublicKey)}
}

// Verify returns ErrSessionInvalid for EVERY failure class — structure,
// algorithm, signature, shape, expiry — so callers emit one uniform 401.
func (v *Verifier) Verify(token string, now time.Time) (SessionClaims, error) {
	var zero SessionClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return zero, ErrSessionInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(v.pub, []byte(parts[0]+"."+parts[1]), sig) {
		return zero, ErrSessionInvalid
	}
	rawHead, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, ErrSessionInvalid
	}
	var head struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(rawHead, &head) != nil || head.Alg != "EdDSA" {
		// WHY header-alg pinning: the signer only emits EdDSA; accepting any
		// alg string here would invite future downgrade confusions.
		return zero, ErrSessionInvalid
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, ErrSessionInvalid
	}
	var claims SessionClaims
	if json.Unmarshal(rawPayload, &claims) != nil || claims.Email == "" {
		return zero, ErrSessionInvalid
	}
	// Boundary matches joblease: token is dead AT exp, valid strictly before.
	if now.Unix() >= claims.Exp {
		return zero, ErrSessionInvalid
	}
	return claims, nil
}
