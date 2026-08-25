// Package ghauth mints short-lived GitHub App installation tokens with the
// standard library only, and owns the per-installation registry of token
// sources and API clients (plan §5.5.1).
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

const defaultAPIBase = "https://api.github.com"

// mintSkewGuard mirrors the iat=now−60s clock-skew guard and the
// refresh-at-expiry−60s cache margin from the v1 implementation.
const (
	mintSkewGuard = 60 * time.Second
	tokenTTL      = 10 * time.Minute
)

// InstallationTokenSource supplies per-repo-scoped installation tokens,
// refreshing them before expiry. Safe for concurrent use; Token() is
// single-flight (the mutex is held across the network exchange).
type InstallationTokenSource struct {
	mu             sync.Mutex
	appID          string
	installationID int64
	privateKey     *rsa.PrivateKey
	http           *http.Client
	baseURL        string
	now            func() time.Time

	// WHY per-repo cache: tokens are narrowed at mint time to exactly the
	// repo being touched (§2.1), so each (installation, repo) pair caches
	// independently; a bug in one publish path can never touch another repo.
	scoped map[string]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Option customizes a source (test seams: fake API base, injected clock).
type Option func(*sourceOptions)

type sourceOptions struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
	key     *rsa.PrivateKey
	keyFile string
}

// WithBaseURL overrides the GitHub API base (tests point it at httptest).
func WithBaseURL(u string) Option { return func(o *sourceOptions) { o.baseURL = u } }

// WithHTTPClient overrides the mint HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(o *sourceOptions) { o.http = c } }

// WithNow overrides the clock used for JWT iat/exp and expiry math.
func WithNow(now func() time.Time) Option { return func(o *sourceOptions) { o.now = now } }

// WithKey injects an already-parsed private key (registry reuse: parse PEM
// once per process, not once per installation).
func WithKey(key *rsa.PrivateKey) Option { return func(o *sourceOptions) { o.key = key } }

// NewInstallationTokenSource builds a source for one installation.
func NewInstallationTokenSource(appID string, installationID int64, opts ...Option) (*InstallationTokenSource, error) {
	o := &sourceOptions{baseURL: defaultAPIBase}
	for _, opt := range opts {
		opt(o)
	}
	if o.key == nil {
		raw, err := os.ReadFile(o.keyFile)
		if err != nil {
			return nil, fmt.Errorf("ghauth: read app private key: %w", err)
		}
		key, err := ParseRSAPrivateKey(raw)
		if err != nil {
			return nil, err
		}
		o.key = key
	}
	httpClient := o.http
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	now := o.now
	if now == nil {
		now = time.Now
	}
	return &InstallationTokenSource{
		appID:          appID,
		installationID: installationID,
		privateKey:     o.key,
		http:           httpClient,
		baseURL:        strings.TrimRight(o.baseURL, "/"),
		now:            now,
		scoped:         make(map[string]cachedToken),
	}, nil
}

// Token returns a valid repo-scoped installation token, minting a fresh one
// when the cached token is within 60s of expiry. The mutex held across the
// exchange makes concurrent first calls single-flight per source.
func (s *InstallationTokenSource) Token(repo string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := repoName(repo)
	if name == "" {
		return "", fmt.Errorf("ghauth: refusing to mint token for malformed repo %q", repo)
	}
	if cached, ok := s.scoped[name]; ok && s.now().Before(cached.expiresAt.Add(-mintSkewGuard)) {
		return cached.token, nil
	}
	jwt, err := s.mintJWT()
	if err != nil {
		return "", err
	}
	token, expiresAt, err := s.exchange(jwt, name)
	if err != nil {
		return "", err
	}
	s.scoped[name] = cachedToken{token: token, expiresAt: expiresAt}
	return token, nil
}

// mintJWT builds the RS256 JWT GitHub requires for App authentication:
// iss=app id, iat=now-60s clock skew guard, exp=now+10m.
func (s *InstallationTokenSource) mintJWT() (string, error) {
	now := s.now().Add(-mintSkewGuard)
	header := base64RawURLEncode([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{s.appID, now.Unix(), now.Add(tokenTTL).Unix()})
	if err != nil {
		return "", fmt.Errorf("ghauth: marshal claims: %w", err)
	}
	signingInput := header + "." + base64RawURLEncode(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := s.privateKey.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("ghauth: sign jwt: %w", err)
	}
	return signingInput + "." + base64RawURLEncode(sig), nil
}

// exchange narrows the minted token to exactly one repository with checks:
// write permission (plan §2.1) instead of the v1 all-repositories `{}` body.
func (s *InstallationTokenSource) exchange(jwt, repo string) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", s.baseURL, s.installationID)
	body := fmt.Sprintf(`{"repositories":[%q],"permissions":{"checks":"write"}}`, repo)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
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
	var parsed struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("ghauth: decode token response: %w", err)
	}
	if parsed.Token == "" {
		return "", time.Time{}, fmt.Errorf("ghauth: empty installation token")
	}
	return strings.TrimSpace(parsed.Token), parsed.ExpiresAt.UTC(), nil
}

// repoName validates owner/name and returns just the name for the scoping
// body; malformed repos fail closed before any network call.
func repoName(repo string) string {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

// ParseRSAPrivateKey parses a PEM-encoded PKCS8 RSA private key.
func ParseRSAPrivateKey(raw []byte) (*rsa.PrivateKey, error) {
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

func base64RawURLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
