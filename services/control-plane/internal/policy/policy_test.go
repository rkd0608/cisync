package policy

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func rec(id string, version int, status string, sel ScopeSelectors) PolicyRecord {
	return PolicyRecord{
		ID: id, Version: version, Status: status,
		Body: PolicyBody{PolicyID: id, Version: version, ScopeSelectors: sel},
	}
}

func TestResolveSpecificityOrdering(t *testing.T) {
	wildcard := rec("pol_wild", 1, StatusActive, ScopeSelectors{})
	repoSel := rec("pol_repo", 1, StatusActive, ScopeSelectors{Repos: []string{"acme/payments"}})
	pathSel := rec("pol_path", 1, StatusActive, ScopeSelectors{Paths: []string{"services/checkout/**"}})
	reg := RegistryFunc(func() ([]PolicyRecord, error) {
		return []PolicyRecord{wildcard, repoSel, pathSel}, nil
	})
	subject := Subject{Repo: "acme/payments", ChangedPaths: []string{"services/checkout/cart.go"}, RiskClass: "high", Actor: "agent:a1"}

	got, err := Resolve(subject, reg)
	require.NoError(t, err)
	require.Equal(t, "pol_path", got.PolicyID, "paths selector must outrank repos and wildcard")

	// Without matching paths, repo selector wins over wildcard.
	got, err = Resolve(Subject{Repo: "acme/payments", ChangedPaths: []string{"other/x.go"}, RiskClass: "high", Actor: "agent:a1"}, reg)
	require.NoError(t, err)
	require.Equal(t, "pol_repo", got.PolicyID)

	// Without repos or paths, wildcard applies.
	got, err = Resolve(Subject{Repo: "acme/other", ChangedPaths: []string{"other/x.go"}, RiskClass: "low", Actor: "agent:a1"}, reg)
	require.NoError(t, err)
	require.Equal(t, "pol_wild", got.PolicyID)
}

func TestResolveTieBreaksByVersionThenID(t *testing.T) {
	v1 := rec("pol_a", 1, StatusActive, ScopeSelectors{Repos: []string{"acme/payments"}})
	v3 := rec("pol_a", 3, StatusActive, ScopeSelectors{Repos: []string{"acme/payments"}})
	polZ := rec("pol_z", 3, StatusActive, ScopeSelectors{Repos: []string{"acme/payments"}})

	for _, tc := range []struct {
		name  string
		recs  []PolicyRecord
		want  string
		wantV int
	}{
		{"higher version wins", []PolicyRecord{v1, v3}, "pol_a", 3},
		{"greater id wins at equal version", []PolicyRecord{v3, polZ}, "pol_z", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(Subject{Repo: "acme/payments", RiskClass: "low", Actor: "agent:x"}, RegistryFunc(func() ([]PolicyRecord, error) {
				return tc.recs, nil
			}))
			require.NoError(t, err)
			require.Equal(t, tc.want, got.PolicyID)
			require.Equal(t, tc.wantV, got.Version)
		})
	}
}

func TestResolveIgnoresInactiveAndMalformed(t *testing.T) {
	recs := []PolicyRecord{
		rec("pol_draft", 9, StatusDraft, ScopeSelectors{}),
		rec("pol_retired", 9, StatusRetired, ScopeSelectors{}),
		{ID: "", Version: 1, Status: StatusActive},
		rec("pol_zero_version", 0, StatusActive, ScopeSelectors{}),
	}
	_, err := Resolve(Subject{Repo: "r", Actor: "agent:x"}, RegistryFunc(func() ([]PolicyRecord, error) { return recs, nil }))
	require.ErrorIs(t, err, ErrNoActivePolicy)
}

func TestResolveActorAndRiskFilters(t *testing.T) {
	sel := ScopeSelectors{
		RiskClasses: []string{"high", "critical"},
		Actors:      ActorSelectors{Agents: []string{"agent:*"}, Exclude: []string{"agent:docs-writer"}},
	}
	reg := RegistryFunc(func() ([]PolicyRecord, error) { return []PolicyRecord{rec("pol_p", 1, StatusActive, sel)}, nil })

	_, err := Resolve(Subject{Repo: "r", RiskClass: "low", Actor: "agent:a"}, reg)
	require.ErrorIs(t, err, ErrNoActivePolicy, "risk class outside selector must not resolve")

	_, err = Resolve(Subject{Repo: "r", RiskClass: "high", Actor: "agent:docs-writer"}, reg)
	require.ErrorIs(t, err, ErrNoActivePolicy, "excluded actor must not resolve")

	got, err := Resolve(Subject{Repo: "r", RiskClass: "critical", Actor: "agent:any"}, reg)
	require.NoError(t, err)
	require.Equal(t, "pol_p", got.PolicyID)
}

func TestResolveFailsClosed(t *testing.T) {
	_, err := Resolve(Subject{Repo: "r"}, nil)
	require.ErrorIs(t, err, ErrNoActivePolicy)

	_, err = Resolve(Subject{Repo: "r"}, RegistryFunc(func() ([]PolicyRecord, error) { return nil, errors.New("db down") }))
	require.ErrorIs(t, err, ErrRegistryFailed)

	_, err = Resolve(Subject{Repo: "r"}, RegistryFunc(func() ([]PolicyRecord, error) { return nil, nil }))
	require.ErrorIs(t, err, ErrNoActivePolicy)
}

func TestSortRecordsDeterministic(t *testing.T) {
	in := []PolicyRecord{rec("b", 2, StatusActive, ScopeSelectors{}), rec("a", 1, StatusActive, ScopeSelectors{}), rec("b", 1, StatusActive, ScopeSelectors{})}
	SortRecords(in)
	require.Equal(t, []string{"a", "b", "b"}, []string{in[0].ID, in[1].ID, in[2].ID})
	require.Equal(t, 1, in[1].Version)
}

// --- property tests ---

var (
	propRepo   = rapid.SampledFrom([]string{"acme/payments", "acme/docs", "acme/core"})
	propPath   = rapid.SampledFrom([]string{"services/checkout/a.go", "services/other/b.go", "auth/x.go", "docs/readme.md"})
	propRisk   = rapid.SampledFrom([]string{"low", "medium", "high", "critical"})
	propActor  = rapid.SampledFrom([]string{"agent:a1", "agent:docs-writer", "human:rakshit"})
	propStatus = rapid.SampledFrom([]string{StatusDraft, StatusActive, StatusRetired})
	propPolID  = rapid.SampledFrom([]string{"pol_alpha", "pol_beta", "pol_gamma"})
)

func propSubject(t *rapid.T) Subject {
	paths := rapid.SliceOfN(propPath, 0, 3).Draw(t, "paths")
	return Subject{
		Repo:         propRepo.Draw(t, "repo"),
		ChangedPaths: paths,
		RiskClass:    propRisk.Draw(t, "risk"),
		Actor:        propActor.Draw(t, "actor"),
	}
}

func propSelector(t *rapid.T) ScopeSelectors {
	sel := ScopeSelectors{}
	if rapid.Bool().Draw(t, "has_repos") {
		sel.Repos = []string{propRepo.Draw(t, "sel_repo")}
	}
	if rapid.Bool().Draw(t, "has_paths") {
		sel.Paths = []string{propPath.Draw(t, "sel_path")}
	}
	if rapid.Bool().Draw(t, "has_risk") {
		sel.RiskClasses = []string{propRisk.Draw(t, "sel_risk")}
	}
	if rapid.Bool().Draw(t, "has_actors") {
		sel.Actors = ActorSelectors{Agents: []string{"agent:*"}}
		if rapid.Bool().Draw(t, "exclude") {
			sel.Actors.Exclude = []string{"agent:docs-writer"}
		}
	}
	return sel
}

func TestPropertyFailClosedWithoutActivePolicy(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 5).Draw(t, "n")
		recs := make([]PolicyRecord, 0, n)
		for i := 0; i < n; i++ {
			status := propStatus.Draw(t, "status")
			if status == StatusActive {
				status = StatusRetired // force: registry holds NO active version
			}
			id := propPolID.Draw(t, "id")
			version := rapid.IntRange(1, 9).Draw(t, "version")
			recs = append(recs, PolicyRecord{
				ID: id, Version: version, Status: status,
				Body: PolicyBody{PolicyID: id, Version: version, ScopeSelectors: propSelector(t)},
			})
		}
		subject := propSubject(t)
		_, err := Resolve(subject, RegistryFunc(func() ([]PolicyRecord, error) { return recs, nil }))
		require.ErrorIs(t, err, ErrNoActivePolicy, "arbitrary registry without active version must never yield a plan")
	})
}

// eligibleOracle reimplements eligibility minimally to cross-check the winner
// really matched and was maximal.
func TestPropertyResolutionPicksMaximalEligible(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(t, "n")
		recs := make([]PolicyRecord, 0, n)
		for i := 0; i < n; i++ {
			id := propPolID.Draw(t, "id")
			version := rapid.IntRange(1, 5).Draw(t, "version")
			recs = append(recs, PolicyRecord{
				ID: id, Version: version, Status: propStatus.Draw(t, "status"),
				Body: PolicyBody{PolicyID: id, Version: version, ScopeSelectors: propSelector(t)},
			})
		}
		subject := propSubject(t)

		got, err := Resolve(subject, RegistryFunc(func() ([]PolicyRecord, error) { return recs, nil }))
		if err != nil {
			require.ErrorIs(t, err, ErrNoActivePolicy)
			return
		}
		spec, ok := matchSpecificity(got.Body.ScopeSelectors, subject)
		require.True(t, ok, "resolved policy must actually match the subject")
		for _, r := range recs {
			if r.ID == got.PolicyID && r.Version == got.Version {
				continue
			}
			rspec, elig := matchSpecificity(r.Body.ScopeSelectors, subject)
			if !elig || r.Status != StatusActive {
				continue
			}
			if rspec > spec {
				t.Fatalf("more specific policy %s@%d should have won", r.ID, r.Version)
			}
			if rspec == spec && r.Version > got.Version {
				t.Fatalf("higher version %s@%d should have won", r.ID, r.Version)
			}
		}
	})
}

func TestPropertyResolveDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 5).Draw(t, "n")
		recs := make([]PolicyRecord, 0, n)
		for i := 0; i < n; i++ {
			id := propPolID.Draw(t, "id")
			version := rapid.IntRange(1, 5).Draw(t, "version")
			recs = append(recs, PolicyRecord{
				ID: id, Version: version, Status: propStatus.Draw(t, "status"),
				Body: PolicyBody{PolicyID: id, Version: version, ScopeSelectors: propSelector(t)},
			})
		}
		shuffled := append([]PolicyRecord(nil), recs...)
		// Reverse for a deterministic different permutation. WHY not sort:
		// a `return true` comparator violates strict-weak ordering, so the
		// "permutation" was whatever sort happened to produce — sometimes no
		// permutation at all, which made this property vacuous/flaky.
		slices.Reverse(shuffled)
		subject := propSubject(t)
		a, errA := Resolve(subject, RegistryFunc(func() ([]PolicyRecord, error) { return recs, nil }))
		b, errB := Resolve(subject, RegistryFunc(func() ([]PolicyRecord, error) { return shuffled, nil }))
		require.Equal(t, errA == nil, errB == nil)
		require.Equal(t, a, b, "same inputs in different registry order must resolve identically")
	})
}
