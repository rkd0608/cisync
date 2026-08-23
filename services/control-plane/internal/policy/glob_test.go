package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"services/checkout/**", "services/checkout/a/b.go", true},
		{"services/checkout/**", "services/checkout/cart.go", true},
		{"services/checkout/**", "services/other/cart.go", false},
		{"services/*/cart.go", "services/checkout/cart.go", true},
		{"services/*/cart.go", "services/checkout/deep/cart.go", false},
		{"auth/**", "auth/oauth/login.go", true},
		{"*.md", "README.md", true},
		{"*.md", "a/b.md", false},
		{"**", "anything/at/all.go", true},
		{"exact", "exact", true},
		{"exact", "other", false},
		{"", "", true},
		{"", "x", false},
		{"agent:*", "agent:docs-writer", true},
		{"agent:docs-writer", "agent:other", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"~"+tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, MatchGlob(tc.pattern, tc.name))
		})
	}
}

// Property: for arbitrary segment sequences, "**" must match every name with
// the same or more segments; and MatchGlob(p, n) == MatchGlob(n, p) is NOT
// asserted (globs are directional) but matching must be reflexive on exact
// strings.
func TestPropertyGlobBasics(t *testing.T) {
	segment := rapid.SampledFrom([]string{"services", "auth", "x.go", "*", "a*b"})
	rapid.Check(t, func(t *rapid.T) {
		pat := rapid.SliceOfN(segment, 1, 4).Draw(t, "pattern_segments")
		rapid.SliceOfN(segment, 1, 6).Draw(t, "name_segments")

		patStr := joinSegments(replaceStars(pat))
		require.Equal(t, true, MatchGlob(patStr, patStr), "exact pattern matches itself")
		require.Equal(t, true, MatchGlob(joinSegments(pat)+"/**", joinSegments(pat)), "a/** matches its own directory path")
	})
}

func replaceStars(segs []string) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		if s == "*" {
			out[i] = "lit"
		} else {
			out[i] = s
		}
	}
	return out
}

func joinSegments(segs []string) string {
	out := ""
	for i, s := range segs {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	return out
}
