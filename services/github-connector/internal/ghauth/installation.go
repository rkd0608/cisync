// Package ghauth mints short-lived GitHub App installation tokens with the
// standard library only, and owns the per-installation registry of token
// sources and API clients (plan §5.5.1).
package ghauth

import (
	"crypto/rsa"
	"crypto/x509"
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

	// issuesWrite mirrors WithIssuesWriteScope so comment writes ride the
	// same scoped token instead of a second mint per request.
	issuesWrite bool
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Option customizes a source (test seams: fake API base, injected clock).
type Option func(*sourceOptions)

type sourceOptions struct {
	baseURL     string
	http        *http.Client
	now         func() time.Time
	key         *rsa.PrivateKey
	keyFile     string
	issuesWrite bool // sticky-comment opt-in; see WithIssuesWriteScope
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

// WithIssuesWriteScope makes every minted repo-scoped token ALSO request
// issues:write — REQUIRED for the sticky verification comment. It only takes
// effect after the operator grants "Issues: Read & write" on the GitHub App;
// otherwise token exchange fails loudly (GitHub rejects ungranted scopes).
func WithIssuesWriteScope() Option {
	return func(o *sourceOptions) { o.issuesWrite = true }
}

// WithHTTPClientOrDefault overrides the mint HTTP client only when non-nil
// (used when replaying stored registry options into lazy source builds).
func WithHTTPClientOrDefault(c *http.Client) Option {
	return func(o *sourceOptions) {
		if c != nil {
			o.http = c
		}
	}
}

// WithNowIfSet overrides the clock only when non-nil (lazy build replay).
func WithNowIfSet(now func() time.Time) Option {
	return func(o *sourceOptions) {
		if now != nil {
			o.now = now
		}
	}
}

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
		issuesWrite:    o.issuesWrite,
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

// ParseRSAPrivateKey parses a PEM-encoded RSA private key in either PKCS#1
// ("RSA PRIVATE KEY" — the format GitHub's "Download private key" emits) or
// PKCS#8 ("PRIVATE KEY").
func ParseRSAPrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("ghauth: app private key is not PEM encoded")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ghauth: parse pkcs1 app private key: %w", err)
		}
		return key, nil
	default:
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
}
