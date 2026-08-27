package authusers

import (
	"strings"
	"testing"
)

// matrix drives TestHashPasswordWeakPasswordPolicy: every entry must be
// REJECTED at hash time (fail-closed) so a sub-threshold password can never
// reach the users table even through a future caller bug.
var weakMatrix = []struct {
	name string
	pass string
}{
	{"empty", ""},
	{"too_short_9", "abcdefghi"},
	{"exactly_below_floor", "123456789"},
	{"all_spaces", "          "},
}

// strongSet pins the accept boundary just ABOVE the floor plus long/unicode
// inputs, guarding against accidental off-by-one and unicode-length bugs.
var strongSet = []string{
	"0123456789",          // exactly 10
	"a longer passphrase with punctuation!", // well above floor
	"pässwörd-ünïcode-10",
	strings.Repeat("x", 200),
}

func TestPasswordRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("hash format missing argon2id parameter header: %s", hash)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("correct password failed verification")
	}
	if VerifyPassword("wrong password entirely", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestPasswordUniqueSalts(t *testing.T) {
	a, err := HashPassword("same-input-password")
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := HashPassword("same-input-password")
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of one input matched; salt reuse breaks rainbow resistance")
	}
	if !VerifyPassword("same-input-password", a) || !VerifyPassword("same-input-password", b) {
		t.Fatal("cross-salt verification failed")
	}
}

func TestWeakPasswordMatrixRejected(t *testing.T) {
	for _, tc := range weakMatrix {
		if _, err := HashPassword(tc.pass); err == nil {
			t.Errorf("%s (%q): accepted, want ErrWeakPassword", tc.name, tc.pass)
		}
	}
	for _, pass := range strongSet {
		if _, err := HashPassword(pass); err != nil {
			t.Errorf("%d-char input rejected: %v", len(pass), err)
		}
	}
}

func TestVerifyGarbageHashFailsClosed(t *testing.T) {
	for _, bad := range []string{"", "not-a-hash", "$argon2id$garbage", strings.Repeat("$argon2id$v=19$m=65536,t=3,p=1$x$y$", 100)} {
		if VerifyPassword("anything", bad) {
			t.Errorf("garbage hash %q verified true; must fail closed", bad[:min(20, len(bad))])
		}
	}
}
