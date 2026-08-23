package policy

import "strings"

// MatchGlob reports whether name matches pattern. Pattern segments are
// separated by "/"; "*" matches any run of characters within one segment and
// a "**" segment matches zero or more whole segments. An empty name never
// matches a non-empty pattern.
func MatchGlob(pattern, name string) bool {
	if pattern == "" {
		return name == ""
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
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

// matchOneSegment is the classic two-pointer wildcard match for one path
// segment; "*" spans any bytes except the "/" separator (never present here).
func matchOneSegment(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		if pi < len(pattern) && (pattern[pi] == s[si]) {
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
