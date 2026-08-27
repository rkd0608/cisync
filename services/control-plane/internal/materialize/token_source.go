package materialize

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// TokenSource mints per-repo scoped GitHub tokens. The App variant requests
// contents:read for EXACTLY one repository per mint (least privilege, GH App
// installation token semantics); the static variant is an honest fallback
// that cannot narrow scope and is documented as such.
type TokenSource interface {
	InstallationToken(ctx context.Context, repo string) (string, error)
}

// StaticTokenSource passes a pre-provisioned token through verbatim. Scope
// narrowing is impossible client-side; operators should prefer the App path.
type StaticTokenSource struct {
	Token string
}

// NewTokenSource resolves operator config into the strongest available token
// source. ALL-or-nothing: half-configured App credentials refuse to boot
// instead of minting unscoped tokens by accident.
func NewTokenSource(appID int64, appKeyFile string, installationID int64, staticToken string) (TokenSource, error) {
	appPartials := 0
	for _, v := range []bool{appID > 0, appKeyFile != "", installationID > 0} {
		if v {
			appPartials++
		}
	}
	switch {
	case appPartials == 3:
		keyPEM, err := os.ReadFile(appKeyFile)
		if err != nil {
			return nil, fmt.Errorf("materialize: read app key file: %w", err)
		}
		return NewGitHubAppSource(appID, keyPEM, installationID, "https://api.github.com/")
	case appPartials > 0 && staticToken != "":
		return nil, fmt.Errorf("materialize: configure EITHER the full GitHub App trio OR a static token, not both")
	case staticToken != "":
		return &StaticTokenSource{Token: staticToken}, nil
	default:
		return nil, fmt.Errorf("materialize: no credential source configured")
	}
}

// InstallationToken implements TokenSource.
func (s *StaticTokenSource) InstallationToken(_ context.Context, _ string) (string, error) {
	if s.Token == "" {
		return "", fmt.Errorf("materialize: static token source has no token configured")
	}
	return s.Token, nil
}

// GitHubAppSource mints short-lived, repo-scoped installation tokens by
// signing a RS256 App JWT with the App private key.
type GitHubAppSource struct {
	appID          int64
	installationID int64
	key            *rsa.PrivateKey // parsed once at construction, fail-fast
	baseURL        string          // trailing slash included
	http           *http.Client
}

// NewGitHubAppSource loads the App private key PEM eagerly (fail fast at boot)
// so a corrupt credential cannot surface only at first dispatch.
func NewGitHubAppSource(appID int64, keyPEM []byte, installationID int64, apiBase string) (*GitHubAppSource, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("materialize: app key PEM invalid")
	}
	rsaKey := parseRSAPrivateKey(block.Bytes)
	if rsaKey == nil {
		return nil, fmt.Errorf("materialize: app key must be PKCS#1 or PKCS#8 RSA")
	}
	if !strings.HasSuffix(apiBase, "/") {
		apiBase += "/"
	}
	return &GitHubAppSource{
		appID: appID, installationID: installationID, key: rsaKey, baseURL: apiBase,
		http: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// parseRSAPrivateKey tolerates both PEM body encodings GitHub issues keys in.
func parseRSAPrivateKey(der []byte) *rsa.PrivateKey {
	if k, e := x509.ParsePKCS1PrivateKey(der); e == nil {
		return k
	}
	k8, e8 := x509.ParsePKCS8PrivateKey(der)
	if e8 != nil {
		return nil
	}
	rsaKey, ok := k8.(*rsa.PrivateKey)
	if !ok {
		return nil
	}
	return rsaKey
}

type jwtClaims struct {
	Iss string `json:"iss"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// InstallationToken signs an App JWT then exchanges it for a token scoped to
// the single requested repo with contents:read only. Tokens are never logged.
func (g *GitHubAppSource) InstallationToken(ctx context.Context, repo string) (string, error) {
	signedJWT, err := g.signAppJWT()
	if err != nil {
		return "", fmt.Errorf("materialize: app jwt: %w", err)
	}
	payload := map[string]any{
		"repositories": []string{repo},
		"permissions":  map[string]string{"contents": "read"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%sapp/installations/%d/access_tokens", g.baseURL, g.installationID),
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+signedJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("materialize: token mint: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("materialize: token mint status %d", resp.StatusCode)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if unmarshalErr := json.Unmarshal(raw, &minted); unmarshalErr != nil {
		return "", fmt.Errorf("materialize: token mint decode: %w", unmarshalErr)
	}
	if minted.Token == "" {
		return "", fmt.Errorf("materialize: token mint returned no token")
	}
	return minted.Token, nil
}

// signAppJWT builds {alg:RS256,typ:JWT} with iss=App id and the documented
// ≤10 minute lifetime (60s backward skew tolerated).
func (g *GitHubAppSource) signAppJWT() (string, error) {
	key := g.key
	if key == nil {
		return "", fmt.Errorf("materialize: app key unavailable")
	}
	now := time.Now().Unix()
	claims, _ := json.Marshal(jwtClaims{Iss: fmt.Sprintf("%d", g.appID), Iat: now - 60, Exp: now + 9*60})
	header := []byte(`{"alg":"RS256","typ":"JWT"}`)
	b64 := base64.RawURLEncoding
	unsigned := b64.EncodeToString(header) + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + b64.EncodeToString(sig), nil
}
