package policy

import (
	"fmt"
	"sort"
)

// Specificity ranks selector dimensions per §8: paths > repos > wildcard.
const (
	specificityWildcard = 0
	specificityRepos    = 2
	specificityPaths    = 4
)

// Resolve picks the most-specific active policy matching subject:
// specificity (paths > repos > wildcard), then highest version, then
// lexicographically greatest ID for total determinism. With zero eligible
// records it fails closed with ErrNoActivePolicy (I-09).
func Resolve(subject Subject, reg Registry) (ResolvedPolicy, error) {
	if reg == nil {
		return ResolvedPolicy{}, fmt.Errorf("%w: nil registry for repo %q risk %q", ErrNoActivePolicy, subject.Repo, subject.RiskClass)
	}
	recs, err := reg.ActivePolicies()
	if err != nil {
		return ResolvedPolicy{}, fmt.Errorf("%w: %v", ErrRegistryFailed, err)
	}
	best := -1
	bestSpec := -1
	for i, rec := range recs {
		if rec.Status != StatusActive || rec.Body.PolicyID == "" || rec.Version <= 0 {
			continue
		}
		spec, ok := matchSpecificity(rec.Body.ScopeSelectors, subject)
		if !ok {
			continue
		}
		if better(spec, rec, bestSpec, best, recs) {
			best, bestSpec = i, spec
		}
	}
	if best < 0 {
		return ResolvedPolicy{}, fmt.Errorf("%w: repo %q risk %q actor %q", ErrNoActivePolicy, subject.Repo, subject.RiskClass, subject.Actor)
	}
	win := recs[best]
	return ResolvedPolicy{PolicyID: win.ID, Version: win.Version, Body: win.Body}, nil
}

func better(spec int, rec PolicyRecord, bestSpec int, best int, recs []PolicyRecord) bool {
	if best < 0 {
		return true
	}
	if spec != bestSpec {
		return spec > bestSpec
	}
	if rec.Version != recs[best].Version {
		return rec.Version > recs[best].Version
	}
	return rec.ID > recs[best].ID
}

// matchSpecificity reports whether all present selector dimensions match the
// subject and, if so, the specificity rank of the match.
func matchSpecificity(sel ScopeSelectors, s Subject) (int, bool) {
	if len(sel.Repos) > 0 && !matchAnyGlob(sel.Repos, s.Repo) {
		return specificityWildcard, false
	}
	if len(sel.Paths) > 0 {
		matched := false
		for _, p := range s.ChangedPaths {
			if matchAnyGlob(sel.Paths, p) {
				matched = true
				break
			}
		}
		if !matched {
			return specificityWildcard, false
		}
	}
	if len(sel.RiskClasses) > 0 && !containsExact(sel.RiskClasses, s.RiskClass) {
		return specificityWildcard, false
	}
	if len(sel.Actors.Agents) > 0 && !matchAnyGlob(sel.Actors.Agents, s.Actor) {
		return specificityWildcard, false
	}
	if len(sel.Actors.Exclude) > 0 && matchAnyGlob(sel.Actors.Exclude, s.Actor) {
		return specificityWildcard, false
	}
	switch {
	case len(sel.Paths) > 0:
		return specificityPaths, true
	case len(sel.Repos) > 0:
		return specificityRepos, true
	default:
		return specificityWildcard, true
	}
}

func matchAnyGlob(patterns []string, value string) bool {
	for _, p := range patterns {
		if MatchGlob(p, value) {
			return true
		}
	}
	return false
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// SortRecords orders policy records deterministically (id, then version) so
// registry adapters can persist or log them reproducibly.
func SortRecords(recs []PolicyRecord) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].ID != recs[j].ID {
			return recs[i].ID < recs[j].ID
		}
		return recs[i].Version < recs[j].Version
	})
}
