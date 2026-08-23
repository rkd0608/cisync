package scheduler

import "sort"

// RankedRun pairs a run with its computed effective priority.
type RankedRun struct {
	Run               Run
	EffectivePriority float64
}

// LessRanked implements the I-13 total order: higher effective priority
// first; ties broken by older ledger sequence first, then lexicographically
// smaller ULID. The order is total (ULIDs are unique) so sorting any
// permutation of the same runs yields the identical sequence.
func LessRanked(a, b RankedRun) bool {
	if a.EffectivePriority != b.EffectivePriority {
		return a.EffectivePriority > b.EffectivePriority
	}
	if a.Run.CreatedSeq != b.Run.CreatedSeq {
		return a.Run.CreatedSeq < b.Run.CreatedSeq
	}
	return a.Run.CreatedULID < b.Run.CreatedULID
}

// SortRanked orders runs in place per I-13. It uses a stable sort; the
// comparator is already total, so output is permutation-invariant.
func SortRanked(rs []RankedRun) {
	sort.SliceStable(rs, func(i, j int) bool { return LessRanked(rs[i], rs[j]) })
}
