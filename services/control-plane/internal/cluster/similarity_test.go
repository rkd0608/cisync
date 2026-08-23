package cluster

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestJaccardKnownValues(t *testing.T) {
	require.InDelta(t, 1.0, Jaccard([]string{"a", "b"}, []string{"b", "a"}), 1e-12)
	require.InDelta(t, 1.0/3.0, Jaccard([]string{"a", "b"}, []string{"b", "c"}), 1e-12)
	require.InDelta(t, 0.0, Jaccard([]string{"a"}, []string{"b"}), 1e-12)
	require.InDelta(t, 0.0, Jaccard(nil, nil), 1e-12, "empty union must not look identical")
}

func TestTrigramSimilarityKnownValues(t *testing.T) {
	require.InDelta(t, 1.0, TrigramSimilarity("services/cart/cart.go", "services/cart/cart.go"), 1e-12)
	sim := TrigramSimilarity("services/cart/cart.go", "services/cart/totals.go")
	require.Greater(t, sim, 0.5)
	less := TrigramSimilarity("services/cart/cart.go", "docs/readme.md")
	require.Less(t, less, sim)
}

func TestPropertySimilaritySymmetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b := genPaths(t), genPaths(t)
		require.Equal(t, Jaccard(a, b), Jaccard(b, a))
		sa, sb := JoinPaths(a), JoinPaths(b)
		require.Equal(t, TrigramSimilarity(sa, sb), TrigramSimilarity(sb, sa))
	})
}
