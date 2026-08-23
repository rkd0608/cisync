package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxBodyBytes caps request bodies (413 beyond).
const maxBodyBytes = 10 << 20

// verifyHMAC checks "sha256=<hex hmac of raw body>" signatures.
func verifyHMAC(secret string, raw []byte, header string) bool {
	const sigPrefix = "sha256="
	if !strings.HasPrefix(header, sigPrefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, sigPrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	return hmac.Equal(mac.Sum(nil), got)
}

// readRawBody reads and size-caps the request body; writes 413 on overflow.
func (s *Server) readRawBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, "validation_failed", "payload too large", nil, nil, nil)
			s.metrics.Inc("sauron_ctrl_http_requests_total", "413")
			return nil, false
		}
		WriteError(w, http.StatusBadRequest, "validation_failed", "unreadable body", nil, nil, nil)
		return nil, false
	}
	return raw, true
}

// requestHash derives the idempotency request hash over the raw body.
func requestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:]))
}
