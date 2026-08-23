// Package api holds the connector HTTP surface (stdlib ServeMux only).
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const signaturePrefix = "sha256="

// ErrBadSignature reports an HMAC mismatch or malformed signature header.
var ErrBadSignature = errors.New("api: bad signature")

// VerifyHMAC checks a "sha256=<hex>" header against raw body bytes using a
// constant-time comparison.
func VerifyHMAC(secret []byte, body []byte, header string) bool {
	if !strings.HasPrefix(header, signaturePrefix) {
		return false
	}
	given, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(given, mac.Sum(nil))
}

// SignHMAC renders the sha256=<hex> signature for outgoing requests.
func SignHMAC(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// readBody enforces the payload size cap at the boundary.
func readBody(w http.ResponseWriter, r io.ReadCloser, capBytes int64) ([]byte, error) {
	buf, err := io.ReadAll(http.MaxBytesReader(w, r, capBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err
		}
		return nil, errors.New("api: body read failed")
	}
	return buf, nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
