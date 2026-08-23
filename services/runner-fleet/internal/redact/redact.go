// Package redact implements the log-output secret scrubber required by
// THREAT_MODEL B3: no secret ever reaches a log sink.
package redact

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

const redactedPlaceholder = "[REDACTED]"

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

// String scrubs every secret-looking substring from s.
func String(s string) string {
	for _, re := range valuePatterns {
		s = re.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
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
