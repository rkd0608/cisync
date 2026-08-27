package ghauth

// This file owns the token MINTING wire surface: the RS256 App JWT, the
// per-repo exchange, and the permission body. Split from installation.go so
// the file cap holds while keeping mint semantics in one place.

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// repoName validates owner/name and returns just the name for the scoping
// body; malformed repos fail closed before any network call.
func repoName(repo string) string {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

// mintJWT builds the RS256 JWT GitHub requires for App authentication:
// iss=app id, iat=now−60s clock skew guard, exp=now+10m.
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
// write permission (plan §2.1). When the sticky-comment feature is opted in,
// the same scoped token also carries issues:write — GitHub rejects any scope
// beyond the App's granted permissions, so enabling WITHOUT bumping App
// settings fails LOUDLY here instead of silently dropping comments.
func (s *InstallationTokenSource) exchange(jwt, repo string) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", s.baseURL, s.installationID)
	body := fmt.Sprintf(`{"repositories":[%q],"permissions":{%s}}`, repo, s.permissionBody())
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
		return "", time.Time{}, fmt.Errorf(
			"ghauth: token exchange status %d (if issues:write was requested, verify the App "+
				"grants it under Settings ▸ Developer settings ▸ GitHub Apps)", resp.StatusCode)
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

func (s *InstallationTokenSource) permissionBody() string {
	if s.issuesWrite {
		return `"checks":"write","issues":"write"`
	}
	return `"checks":"write"`
}

func base64RawURLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
