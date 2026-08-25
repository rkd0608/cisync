// Package api holds the ingest HTTP handlers (stdlib ServeMux only).
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VerifyGitHubSignature checks an X-Hub-Signature-256 header value against the
// raw body using a constant-time comparison.
func VerifyGitHubSignature(secret []byte, body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	given, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(given, mac.Sum(nil))
}

// VerifyGitHubSignatureAny checks the header against EVERY secret in the
// rotation list (EC-010): each candidate runs a constant-time compare and ANY
// match passes, so deliveries signed with the old secret stay valid during
// the overlap window. An empty list fails closed.
func VerifyGitHubSignatureAny(secrets [][]byte, body []byte, header string) bool {
	for _, secret := range secrets {
		if VerifyGitHubSignature(secret, body, header) {
			return true
		}
	}
	return false
}

// VerifyTimestampSkew validates the optional X-Sauron-Timestamp header. When
// the header is absent verification passes; when present the skew must be
// within tolerance (replay window).
func VerifyTimestampSkew(header string, now time.Time, tolerance time.Duration) error {
	if header == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339, header)
	if err != nil {
		if n, perr := parseUnixMillis(header); perr == nil {
			ts = n
		} else {
			return fmt.Errorf("api: malformed X-Sauron-Timestamp: %w", err)
		}
	}
	skew := now.Sub(ts)
	if skew < 0 {
		skew = -skew
	}
	if skew > tolerance {
		return fmt.Errorf("api: timestamp skew %s exceeds tolerance %s", skew, tolerance)
	}
	return nil
}

func parseUnixMillis(s string) (time.Time, error) {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("api: not unix millis: %w", err)
	}
	return time.UnixMilli(ms), nil
}
