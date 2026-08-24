package joblease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// Verifier validates lease tokens with a public key only. Verify returns
// ErrInvalid (opaque) for every failure class — signature, audience, expiry,
// or shape — so handlers cannot leak which check failed.
type Verifier struct {
	pub ed25519.PublicKey
	// Now is injectable so protocol tests can pin expiry semantics.
	Now func() time.Time
}

// NewVerifierFromPublicPEM builds the verify-only validator from an SPKI
// "-----BEGIN PUBLIC KEY-----" PEM (the fleet's only credential input).
func newVerifier(publicPEM []byte) (*Verifier, error) {
	block, _ := pem.Decode(publicPEM)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("joblease: no PUBLIC KEY block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("joblease: parse spki: %w", err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("joblease: key is %T, want ed25519.PublicKey", parsed)
	}
	return &Verifier{pub: pub, Now: time.Now}, nil
}

// NewVerifierFromPublicPEM builds the verify-only validator from an SPKI
// "-----BEGIN PUBLIC KEY-----" PEM (the fleet's only credential input).
func NewVerifierFromPublicPEM(publicPEM []byte) (*Verifier, error) {
	return newVerifier(publicPEM)
}

// Verify checks compact-token structure, Ed25519 signature, aud and exp.
func (v *Verifier) Verify(token string) (Claims, error) {
	headerB64, payloadB64, sigB64, ok := splitCompact(token)
	if !ok {
		return Claims{}, ErrInvalid
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := decodeSegment(headerB64, &header); err != nil || header.Alg != "EdDSA" {
		return Claims{}, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !ed25519.Verify(v.pub, []byte(headerB64+"."+payloadB64), sig) {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decodeSegment(payloadB64, &claims); err != nil {
		return Claims{}, ErrInvalid
	}
	if claims.Audience != Audience {
		return Claims{}, ErrInvalid
	}
	if v.nowUnix() >= claims.ExpiresAt {
		return Claims{}, ErrInvalid
	}
	if claims.RunID == "" || claims.Attempt < 1 || claims.ID != JTIBuilds(claims.RunID, claims.Attempt, claims.FenceToken) {
		return Claims{}, ErrInvalid
	}
	return claims, nil
}

// FromAuthorizationHeader extracts the raw token from an "Authorization:
// Bearer <token>" header value.
func FromAuthorizationHeader(header string) (string, bool) {
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	token := strings.TrimSpace(header[len(scheme):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func (v *Verifier) nowUnix() int64 { return v.Now().Unix() }

func splitCompact(token string) (string, string, string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func decodeSegment(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return fmt.Errorf("joblease: base64url segment: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("joblease: segment json: %w", err)
	}
	return nil
}

func signCompact(priv ed25519.PrivateKey, claims Claims) (string, error) {
	header := []byte(`{"alg":"EdDSA","typ":"JWT"}`)
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("joblease: marshal claims: %w", err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(header)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(priv, []byte(headerB64+"."+payloadB64))
	return headerB64 + "." + payloadB64 + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
