package cluster

// ElectRepresentative returns the cluster representative: argmax priority,
// ties broken by oldest ledger sequence first, then lexicographically
// smallest ID — the same priority→age→ULID order as scheduler tie-breaks
// (I-13). The second return is false for an empty input. Callers must pass
// only live members; the representative is always a live member (§1.3).
func ElectRepresentative(members []Member) (Member, bool) {
	var best Member
	found := false
	for _, m := range members {
		if !found || betterRep(m, best) {
			best, found = m, true
		}
	}
	return best, found
}

func betterRep(a, b Member) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if a.CreatedSeq != b.CreatedSeq {
		return a.CreatedSeq < b.CreatedSeq
	}
	return a.ID < b.ID
}
