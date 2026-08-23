package planner

// SurfaceClasses derives crude surface classes from changed paths: the first
// path segment ("_root" for repo-root files). Sorted and deduplicated.
func SurfaceClasses(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	var out []string
	for _, p := range paths {
		class := "_root"
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				class = p[:i]
				break
			}
		}
		if _, dup := seen[class]; dup {
			continue
		}
		seen[class] = struct{}{}
		out = append(out, class)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// effectiveSelectionConfidence clamps the impact model's confidence into
// [0,1], defaulting to the no-history maximum-uncertainty value.
func effectiveSelectionConfidence(in CandidateInput) float64 {
	if in.SelectionConfidence == nil {
		return NoHistorySelectionConfidence
	}
	c := *in.SelectionConfidence
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}
