package cluster

import "sort"

// StrategyVersionV0 stamps assignments so historical clusters stay
// interpretable after algorithm changes (§1.3 strategy_version).
const StrategyVersionV0 = "clustering-v0"

// Membership thresholds (DOMAIN_MODEL_DRAFT §2): join iff path-overlap ≥ 0.6
// AND trigram similarity ≥ τ. The duplicate label additionally requires
// symbol overlap ≥ θ. τ and θ default to the same 0.6 in v0; the spec leaves
// them tunable.
const (
	PathOverlapThreshold   = 0.6
	TrigramThreshold       = 0.6
	SymbolOverlapThreshold = 0.6
)

// relation_to_rep values, matching domain.Relation exactly.
const (
	RelationDuplicateOf   = "duplicate_of"
	RelationAlternativeOf = "alternative_of"
	RelationComposable    = "composable_with"
	RelationConflicting   = "conflicts_with"
	RelationPrerequisite  = "prerequisite_of"
	RelationNone          = ""
)

// Member is the clustering view of a candidate. CreatedSeq is the ledger
// sequence at submission (logical clock; wall clocks are advisory).
type Member struct {
	ID             string
	ChangedPaths   []string
	ChangedSymbols []string
	DependsOn      []string // declared prerequisite candidate IDs
	Priority       float64
	CreatedSeq     int64
}

// MemberWithRelation is a cluster member plus its current relation_to_rep.
type MemberWithRelation struct {
	Member        Member
	RelationToRep string
}

// ActiveCluster is an active cluster snapshot for assignment decisions.
type ActiveCluster struct {
	ID      string
	RepoID  string
	Rep     Member
	Members []MemberWithRelation
}

// Assignment is the deterministic clustering decision for one candidate.
type Assignment struct {
	Joined            bool
	ClusterID         string // existing cluster when Joined, else ""
	RelationToRep     string // relation value or RelationNone when not joined
	PathOverlap       float64
	TrigramSimilarity float64
	SymbolOverlap     float64
	StrategyVersion   string
}

// Assign decides whether candidate c joins one of the active clusters of its
// repo and, if so, classifies its relation to that cluster's representative.
//
// Join rule: path-overlap (Jaccard) ≥ 0.6 AND trigram similarity ≥ τ against
// the representative. Among qualifying clusters the best match wins by
// (path-overlap desc, trigram desc, cluster id asc) — fully deterministic.
//
// Relation heuristic v0 against the elected representative, in precedence
// order: duplicate_of when symbol overlap and trigram both clear their
// thresholds; prerequisite_of when the rep appears in DependsOn;
// conflicts_with when both touch at least one identical file; otherwise
// alternative_of. Composable_with is never produced intra-cluster: disjoint
// surfaces cannot clear the join threshold (composable edges arise
// cross-cluster). Prerequisite detection uses declared dependency edges only;
// dep-graph inference is a later wave.
func Assign(c Member, clusters []ActiveCluster) Assignment {
	type scored struct {
		idx int
		po  float64
		tri float64
	}
	var candidates []scored
	for i, cl := range clusters {
		if cl.RepoID == "" && len(clusters) > 0 && cl.ID == "" {
			continue
		}
		po := Jaccard(c.ChangedPaths, cl.Rep.ChangedPaths)
		tri := TrigramSimilarity(JoinPaths(c.ChangedPaths), JoinPaths(cl.Rep.ChangedPaths))
		if po >= PathOverlapThreshold && tri >= TrigramThreshold {
			candidates = append(candidates, scored{idx: i, po: po, tri: tri})
		}
	}
	if len(candidates) == 0 {
		return Assignment{Joined: false, StrategyVersion: StrategyVersionV0}
	}
	sort.SliceStable(candidates, func(x, y int) bool {
		a, b := candidates[x], candidates[y]
		if a.po != b.po {
			return a.po > b.po
		}
		if a.tri != b.tri {
			return a.tri > b.tri
		}
		return clusters[a.idx].ID < clusters[b.idx].ID
	})
	best := candidates[0]
	cl := clusters[best.idx]
	sym := Jaccard(c.ChangedSymbols, cl.Rep.ChangedSymbols)
	return Assignment{
		Joined:            true,
		ClusterID:         cl.ID,
		RelationToRep:     classify(c, cl.Rep, sym, best.tri),
		PathOverlap:       best.po,
		TrigramSimilarity: best.tri,
		SymbolOverlap:     sym,
		StrategyVersion:   StrategyVersionV0,
	}
}

func classify(c, rep Member, symOverlap, trigram float64) string {
	if len(c.ChangedSymbols) > 0 && len(rep.ChangedSymbols) > 0 &&
		symOverlap >= SymbolOverlapThreshold && trigram >= TrigramThreshold {
		return RelationDuplicateOf
	}
	for _, dep := range c.DependsOn {
		if dep == rep.ID {
			return RelationPrerequisite
		}
	}
	if sharesExactPath(c.ChangedPaths, rep.ChangedPaths) {
		return RelationConflicting
	}
	return RelationAlternativeOf
}

func sharesExactPath(a, b []string) bool {
	setB := make(map[string]struct{}, len(b))
	for _, x := range b {
		setB[x] = struct{}{}
	}
	for _, x := range a {
		if _, ok := setB[x]; ok {
			return true
		}
	}
	return false
}

// JoinPaths renders a deterministic canonical string for a path set; used as
// trigram input and for stable hashing by adapters.
func JoinPaths(paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	out := ""
	for i, p := range sorted {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}
