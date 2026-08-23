// Package cluster implements clustering v0: candidate-to-cluster assignment
// by path-overlap (Jaccard) plus character-trigram similarity, relation
// classification, representative election and supersede propagation
// decisions (DOMAIN_MODEL_DRAFT §1.3, §2).
package cluster

// Jaccard returns |a ∩ b| / |a ∪ b| over the deduplicated elements of a and
// b. An empty union yields 0 so that two candidates with no changed paths
// are never considered identical.
func Jaccard(a, b []string) float64 {
	setA := make(map[string]struct{}, len(a))
	for _, x := range a {
		setA[x] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, x := range b {
		setB[x] = struct{}{}
	}
	union := len(setA)
	for x := range setB {
		if _, dup := setA[x]; !dup {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	inter := 0
	for x := range setB {
		if _, ok := setA[x]; ok {
			inter++
		}
	}
	return float64(inter) / float64(union)
}

// TrigramSimilarity returns the Jaccard coefficient over character trigrams
// of the two strings. Strings shorter than three runes compare equal only if
// byte-identical.
func TrigramSimilarity(a, b string) float64 {
	ta := trigrams(a)
	tb := trigrams(b)
	return jaccardSets(ta, tb)
}

func trigrams(s string) map[string]struct{} {
	runes := []rune(s)
	out := make(map[string]struct{}, maxInt(0, len(runes)-2))
	if len(runes) < 3 {
		if s != "" {
			out[s] = struct{}{}
		}
		return out
	}
	for i := 0; i+2 < len(runes); i++ {
		out[string(runes[i:i+3])] = struct{}{}
	}
	return out
}

func jaccardSets(a, b map[string]struct{}) float64 {
	union := len(a)
	for x := range b {
		if _, dup := a[x]; !dup {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	inter := 0
	for x := range b {
		if _, ok := a[x]; ok {
			inter++
		}
	}
	return float64(inter) / float64(union)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
