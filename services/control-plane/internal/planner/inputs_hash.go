package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// InputsMaterial is the full reuse key per I-02: base SHA, lockfiles, flags
// and toolchain. Every field participates in the digest.
type InputsMaterial struct {
	BaseSHA   string
	Lockfiles []string
	Flags     []string
	Toolchain string
}

// HashInputs computes the plan-level inputs_hash: "sha256:<hex>" over a
// canonical serialization of the material. Slices are sorted so equivalent
// inputs hash identically regardless of submission order; a change in any
// component changes the digest (changed input ⇒ miss).
func HashInputs(m InputsMaterial) string {
	lockfiles := append([]string(nil), m.Lockfiles...)
	flags := append([]string(nil), m.Flags...)
	sort.Strings(lockfiles)
	sort.Strings(flags)
	var b strings.Builder
	b.WriteString("base_sha=")
	b.WriteString(m.BaseSHA)
	b.WriteString("\nlockfiles=")
	b.WriteString(strings.Join(lockfiles, ","))
	b.WriteString("\nflags=")
	b.WriteString(strings.Join(flags, ","))
	b.WriteString("\ntoolchain=")
	b.WriteString(m.Toolchain)
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}
