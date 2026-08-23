// Package planner constructs validation plans over the Tier 0–4 ladder
// (DOMAIN_MODEL_DRAFT §3), including selection heuristics v0, the seven
// fallback-to-full-suite triggers with "fallback:<trigger>" rationales, and
// deterministic inputs_hash computation (I-02). Plan is a pure function:
// identical inputs produce byte-identical plans.
package planner

// MatchGlob reports whether name matches pattern; identical semantics to
// policy.MatchGlob. Duplicated locally so this package stays dependency-free
// at the type level.
func MatchGlob(pattern, name string) bool {
	return matchSegments(splitPath(pattern), splitPath(name))
}

func splitPath(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 || !matchOneSegment(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

func matchOneSegment(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		if pi < len(pattern) && pattern[pi] == s[si] {
			pi++
			si++
			continue
		}
		if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			mark = si
			pi++
			continue
		}
		if star != -1 {
			mark++
			si = mark
			pi = star + 1
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func matchAnyGlob(patterns []string, value string) bool {
	for _, p := range patterns {
		if MatchGlob(p, value) {
			return true
		}
	}
	return false
}
