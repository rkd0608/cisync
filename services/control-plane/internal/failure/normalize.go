// Package failure implements the failure taxonomy classifier (signature
// digest over a normalized log, DOMAIN_MODEL_DRAFT §1.7 classes) and bounded
// repair envelope authorization (I-05).
package failure

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	isoTSRe    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	clockRe    = regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`)
	durationRe = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|us|ms|s|m|h)\b`)
	hashHexRe  = regexp.MustCompile(`\b[a-fA-F0-9]{8,64}\b`)
	addrRe     = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	floatRe    = regexp.MustCompile(`\b\d+\.\d+\b`)
	intRe      = regexp.MustCompile(`\b\d+\b`)
)

// NormalizeLog reduces a raw runner log to a stable fingerprint input:
// ANSI escapes, timestamps, durations, hex hashes, addresses and numeric
// literals are replaced with fixed tokens and whitespace collapses to single
// spaces. The result is deterministic and order-stable.
func NormalizeLog(logText string) string {
	s := ansiRe.ReplaceAllString(logText, "")
	s = isoTSRe.ReplaceAllString(s, "<ts>")
	s = clockRe.ReplaceAllString(s, "<ts>")
	s = durationRe.ReplaceAllString(s, "<dur>")
	s = hashHexRe.ReplaceAllString(s, "<hex>")
	s = addrRe.ReplaceAllString(s, "<addr>")
	s = floatRe.ReplaceAllString(s, "<n>")
	s = intRe.ReplaceAllString(s, "<n>")
	return strings.Join(strings.Fields(s), " ")
}

// SignatureDigest is the normalized-log fingerprint: sha256 of NormalizeLog,
// rendered as "sha256:<hex>" (§1.7 signature_digest).
func SignatureDigest(logText string) string {
	sum := sha256.Sum256([]byte(NormalizeLog(logText)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
