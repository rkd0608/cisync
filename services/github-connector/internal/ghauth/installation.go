// Package ghauth mints short-lived GitHub App installation tokens with the
// standard library only: the App JWT is signed RS256 via crypto/rsa and the
// token is exchanged against the installations access_tokens endpoint.
package ghauth

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const githubAPIBase = "https://api.github.com"

// InstallationTokenSource supplies installation tokens, refreshing them
// before expiry. Safe for concurrent use.
type InstallationTokenSource struct {
	mu             sync.Mutex
	appID          string
	installationID int64
	privateKey     *rsa.PrivateKey
	http           *http.Client

	token     string
	expiresAt time.Time
}

// NewInstallationTokenSource loads the PEM private key from keyFile.
func NewInstallationTokenSource(appID, keyFile string, installationID int64) (*InstallationTokenSource, error) {
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("ghauth: read app private key: %w", err)
	}
	key, err := parseRSAPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return &InstallationTokenSource{
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		http:           &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Token returns a valid installation token, minting a fresh one when the
// cached token is within 60s of expiry.
func (s *InstallationTokenSource) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expiresAt.Add(-60*time.Second)) {
		return s.token, nil
	}
	jwt, err := s.mintJWT()
	if err != nil {
		return "", err
	}
	token, expiresAt, err := s.exchange(jwt)
	if err != nil {
		return "", err
	}
	s.token = token
	s.expiresAt = expiresAt
	return token, nil
}

// mintJWT builds the RS256 JWT GitHub requires for App authentication:
// iss=app id, iat=now-60s clock skew guard, exp=now+10m.
func (s *InstallationTokenSource) mintJWT() (string, error) {
	now := time.Now().Add(-60 * time.Second)
	header := base64RawURLJSON([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss": s.appID,
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("ghauth: marshal claims: %w", err)
	}
	signingInput := header + "." + base64RawURLJSON(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := s.privateKey.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("ghauth: sign jwt: %w", err)
	}
	return signingInput + "." + base64RawURLEncode(sig), nil
}

func (s *InstallationTokenSource) exchange(jwt string) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBase, s.installationID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("ghauth: build token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("ghauth: token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("ghauth: token exchange status %d", resp.StatusCode)
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("ghauth: decode token response: %w", err)
	}
	if body.Token == "" {
		return "", time.Time{}, fmt.Errorf("ghauth: empty installation token")
	}
	return strings.TrimSpace(body.Token), body.ExpiresAt.UTC(), nil
}

func parseRSAPrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("ghauth: app private key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ghauth: parse app private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ghauth: app private key must be RSA, got %T", parsed)
	}
	return key, nil
}

func base64RawURLJSON(v []byte) string { return base64RawURLEncode(v) }

func base64RawURLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
