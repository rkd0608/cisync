// Package authusers owns user-account credentials for email+password sign-in.
//
// WHY this exists: OTP-email auth was replaced by classic credentials per
// SPEC §3 (2026-08-26 auth-engineer row). Password hashing lives here — NOT
// in the API layer — so the storage layer can treat password_hash as an
// opaque string and no handler can accidentally log or compare plaintexts.
package authusers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (SPEC: memory=64MB, t=3, p=1). WHY explicit consts:
// they are contract-visible inside every stored hash string; changing any of
// them silently makes Verify recompute under DIFFERENT parameters and reject
// every existing account. Rotation requires the versioned re-hash path first.
const (
	argonMemoryKiB = 64 * 1024 // 64 MB
	argonTime      = 3         // passes
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// WeakPasswordError reports that a candidate password violated the minimum
// policy (>= MinPasswordLen chars).
type WeakPasswordError struct{ Reason string }

func (e *WeakPasswordError) Error() string { return "weak_password: " + e.Reason }

// MinPasswordLen is the signup policy floor (SPEC: ≥10 chars).
const MinPasswordLen = 10

// ErrMalformedHash means the stored encoded string is not parseable; treated
// as a verify failure (never as match).
var ErrMalformedHash = errors.New("authusers: malformed password hash")

// HashPassword derives an argon2id PHC-format encoded string:
// $argon2id$v=19$m=$MEM,t=$T,p=$P$<saltB64>$<keyB64>. Salt is freshly random
// per call — identical inputs never produce identical rows (heap-dump +
// rainbow-table resistance).
func HashPassword(password string) (string, error) {
	if n := len(password); n < MinPasswordLen {
		return "", &WeakPasswordError{Reason: fmt.Sprintf("password must be at least %d characters", MinPasswordLen)}
	}
	// WHY strip-whitespace probe: a pure length floor admits a 10-space
	// password; reject inputs with no non-whitespace content at all.
	if strings.TrimSpace(password) == "" {
		return "", &WeakPasswordError{Reason: "password must contain non-whitespace characters"}
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("authusers: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword recomputes the KDF from the STORED encoded parameters and
// compares keys constant-time. Every failure class — malformed string, salt
// decode, unknown params — returns false (WHY fail-closed: any exception path
// that returned true would be an authentication bypass).
func VerifyPassword(password, encoded string) bool {
	fields := strings.Split(encoded, "$")
	// Expect: "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key.
	if len(fields) != 6 || fields[1] != "argon2id" {
		return false
	}
	var v uint32
	if _, err := fmt.Sscanf(fields[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false
	}
	memory, timeParam, threads, ok := parseParams(fields[3])
	if !ok || memory != argonMemoryKiB || timeParam != argonTime || threads != argonThreads {
		// WHY: only hashes minted at the pinned parameters verify. Future
		// param migrations must add explicit legacy-verify support here,
		// never widen this branch into accepting anything.
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil || len(want) != argonKeyLen {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeParam, memory, uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parseParams(field string) (memory, timeParam, threads uint32, ok bool) {
	_, err := fmt.Sscanf(field, "m=%d,t=%d,p=%d", &memory, &timeParam, &threads)
	if err != nil {
		return 0, 0, 0, false
	}
	if memory == 0 || timeParam == 0 || threads == 0 {
		return 0, 0, 0, false
	}
	return memory, timeParam, threads, true
}
