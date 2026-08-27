package materialize

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mustGenerateRSA creates a throwaway 2048-bit key for App-JWT round trips.
func mustGenerateRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return key
}

// pemFromKey serializes to the PKCS#1 PEM form GitHub issues.
func pemFromKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func parseKeyForTest(t *testing.T, pemBytes []byte) *rsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("pem decode failed in fixture")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type jwtParts struct {
	RawHeader, RawClaims string
	Sig                  []byte
	Unsigned             string
}

func decodeJWT(authz string) (jwtParts, error) {
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return jwtParts{}, fmt.Errorf("mint request must carry Bearer app-jwt")
	}
	parts := strings.Split(strings.TrimPrefix(authz, bearer), ".")
	if len(parts) != 3 {
		return jwtParts{}, fmt.Errorf("app jwt must have 3 segments")
	}
	b64 := base64.RawURLEncoding
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return jwtParts{}, fmt.Errorf("jwt sig decode: %w", err)
	}
	return jwtParts{RawHeader: parts[0], RawClaims: parts[1], Sig: sig, Unsigned: parts[0] + "." + parts[1]}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// verifyAppJWT validates signature, alg/typ header and iss/exp claims exactly
// as GitHub's API does server-side.
func verifyAppJWT(authz string, pub *rsa.PublicKey, wantIss int64) error {
	parts, err := decodeJWT(authz)
	if err != nil {
		return err
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts.RawHeader)
	if err != nil {
		return err
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if jsonErr := json.Unmarshal(headerRaw, &header); jsonErr != nil || header.Alg != "RS256" {
		return fmt.Errorf("bad jwt header %s (%v)", headerRaw, jsonErr)
	}
	digest := sha256.Sum256([]byte(parts.Unsigned))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], parts.Sig) != nil {
		return fmt.Errorf("jwt signature invalid")
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts.RawClaims)
	if err != nil {
		return err
	}
	var claims jwtClaims
	if json.Unmarshal(claimsRaw, &claims) != nil {
		return fmt.Errorf("jwt claims undecodable")
	}
	now := time.Now().Unix()
	if claims.Iss != fmt.Sprintf("%d", wantIss) {
		return fmt.Errorf("jwt iss mismatch: %q", claims.Iss)
	}
	if claims.Iat > now || claims.Exp < now {
		return fmt.Errorf("jwt lifetime invalid iat=%d exp=%d now=%d", claims.Iat, claims.Exp, now)
	}
	return nil
}
