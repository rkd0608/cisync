package ghauth

import (
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v66/github"
)

// Registry owns one InstallationTokenSource + go-github client per known
// installation, built lazily on first use behind an RWMutex (plan §5.5.1).
// The configured INSTALLATION_ID seeds the DEFAULT entry at boot so the
// common single-installation deployment never pays first-use build latency.
type Registry struct {
	mu         sync.RWMutex
	appID      string
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
	keyFile    string
	injected   *rsa.PrivateKey

	keyOnce sync.Once
	key     *rsa.PrivateKey
	keyErr  error

	sources map[int64]*InstallationTokenSource
	clients map[int64]*github.Client
}

// NewRegistry builds a registry. keyFile may be empty in dry-run mode; the
// PEM is parsed at most once per process no matter how many installations
// appear (WithKey reuse across sources). WithKey injects a pre-parsed key,
// which takes precedence over keyFile.
func NewRegistry(appID, keyFile string, opts ...Option) *Registry {
	o := &sourceOptions{baseURL: defaultAPIBase}
	for _, opt := range opts {
		opt(o)
	}
	return &Registry{
		appID:      appID,
		baseURL:    strings.TrimRight(o.baseURL, "/"),
		httpClient: o.http,
		now:        o.now,
		keyFile:    keyFile,
		injected:   o.key,
		sources:    make(map[int64]*InstallationTokenSource),
		clients:    make(map[int64]*github.Client),
	}
}

// Seed pre-builds the source for installationID (boot-time wiring of the
// configured default installation). Errors surface here, not lazily.
func (r *Registry) Seed(installationID int64) error {
	_, err := r.Source(installationID)
	return err
}

// Source returns the per-installation token source, building it lazily.
// Double-checked locking keeps single-flight per installation.
func (r *Registry) Source(installationID int64) (*InstallationTokenSource, error) {
	r.mu.RLock()
	src, ok := r.sources[installationID]
	r.mu.RUnlock()
	if ok {
		return src, nil
	}
	key, err := r.loadKey()
	if err != nil {
		return nil, err
	}
	opts := []Option{WithBaseURL(r.baseURL), WithKey(key)}
	if r.httpClient != nil {
		opts = append(opts, WithHTTPClient(r.httpClient))
	}
	if r.now != nil {
		opts = append(opts, WithNow(r.now))
	}
	built, err := NewInstallationTokenSource(r.appID, installationID, opts...)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.sources[installationID]; ok {
		// Lost a build race: keep whichever source won, drop ours.
		return existing, nil
	}
	r.sources[installationID] = built
	return built, nil
}

// Client returns a go-github client whose transport mints repo-scoped tokens
// for the given installation per request. Clients cache per installation.
func (r *Registry) Client(installationID int64) (*github.Client, error) {
	r.mu.RLock()
	client, ok := r.clients[installationID]
	r.mu.RUnlock()
	if ok {
		return client, nil
	}
	if _, err := r.Source(installationID); err != nil {
		return nil, err
	}
	built := github.NewClient(&http.Client{
		Timeout: 15 * time.Second,
		Transport: &repoScopedTransport{
			registry:       r,
			installationID: installationID,
		},
	})
	base, err := url.Parse(r.baseURL + "/")
	if err != nil {
		return nil, fmt.Errorf("ghauth: parse api base %q: %w", r.baseURL, err)
	}
	built.BaseURL = base
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.clients[installationID]; ok {
		return existing, nil // lost a build race
	}
	r.clients[installationID] = built
	return built, nil
}

// loadKey parses the PEM exactly once for all installations.
func (r *Registry) loadKey() (*rsa.PrivateKey, error) {
	if r.injected != nil {
		return r.injected, nil
	}
	r.keyOnce.Do(func() {
		raw, readErr := readFileForTestability(r.keyFile)
		if readErr != nil {
			r.keyErr = fmt.Errorf("ghauth: read app private key: %w", readErr)
			return
		}
		r.key, r.keyErr = ParseRSAPrivateKey(raw)
	})
	return r.key, r.keyErr
}
