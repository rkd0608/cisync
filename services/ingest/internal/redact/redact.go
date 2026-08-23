// Package redact implements the fail-closed secret scrubber required by
// THREAT_MODEL T1/B3: payload keys and value shapes that look like secrets are
// replaced before any forwarding or logging.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const redactedPlaceholder = "[REDACTED]"

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|passwd|authorization|auth|credential|private[_-]?key|api[_-]?key|access[_-]?key|session|cookie|signature)`)

var (
	bearerPattern    = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]{8,}`)
	jwtPattern       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	githubPATPattern = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`)
	awsKeyPattern    = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}`)
	pemBlockPattern  = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	connStrPattern   = regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|redis|mongodb(?:\+srv)?)://[^\s'"]*:[^\s'"@]+@`)
	gcpKeyPattern    = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`)
	kvSecretPattern  = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret|private[_-]?key)["']?\s*[=:]\s*["']?[^\s"',}]+`)
)

var valuePatterns = []*regexp.Regexp{
	bearerPattern, jwtPattern, githubPATPattern, awsKeyPattern,
	pemBlockPattern, connStrPattern, gcpKeyPattern, kvSecretPattern,
}

// KeyIsSensitive reports whether a JSON object key looks secret-bearing.
func KeyIsSensitive(key string) bool {
	return sensitiveKeyPattern.MatchString(key)
}

// String scrubs every secret-looking substring from s.
func String(s string) string {
	for _, re := range valuePatterns {
		s = re.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

// Payload walks a raw JSON document and returns a redacted copy. Object keys
// matching sensitive patterns have their values (recursively) replaced; string
// scalars are additionally scrubbed by value shape. It fails closed: any
// malformed input yields an error plus a minimal tombstone document instead of
// passthrough data.
func Payload(raw []byte) ([]byte, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		tomb, _ := json.Marshal(map[string]any{"redacted": true, "reason": "unredactable_payload"})
		return tomb, fmt.Errorf("redact: payload is not valid json: %w", err)
	}
	scrubbed := walk(doc)
	out, err := json.Marshal(scrubbed)
	if err != nil {
		tomb, _ := json.Marshal(map[string]any{"redacted": true, "reason": "unencodable_payload"})
		return tomb, fmt.Errorf("redact: cannot re-encode payload: %w", err)
	}
	return out, nil
}

func walk(node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if KeyIsSensitive(k) {
				out[k] = redactedPlaceholder
				continue
			}
			out[k] = walk(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = walk(val)
		}
		return out
	case string:
		return String(v)
	default:
		return node
	}
}

// Writer is an io.Writer that scrubs log output so no secret ever reaches a
// log sink (B3).
type Writer struct {
	Next io.Writer
}

// Write implements io.Writer with per-line redaction applied first.
func (w *Writer) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for i, line := range lines {
		lines[i] = String(line)
	}
	if _, err := w.Next.Write([]byte(strings.Join(lines, "\n"))); err != nil {
		return 0, fmt.Errorf("redact: log write failed: %w", err)
	}
	return len(p), nil
}
