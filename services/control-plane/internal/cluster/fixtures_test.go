package cluster

import (
	"pgregory.net/rapid"
)

func member(id string, paths ...string) Member {
	return Member{ID: id, ChangedPaths: paths, ChangedSymbols: []string{id + "_sym"}, CreatedSeq: 1}
}

func singleCluster(rep Member, members ...MemberWithRelation) ActiveCluster {
	return ActiveCluster{ID: "clus_1", RepoID: "acme/payments", Rep: rep, Members: members}
}

// --- shared property generators ---

var genPath = rapid.SampledFrom([]string{
	"services/cart/cart.go", "services/cart/totals.go", "services/auth/login.go",
	"payments/refund.go", "docs/readme.md", "infra/main.tf",
})

func genPaths(t *rapid.T) []string { return rapid.SliceOfN(genPath, 0, 4).Draw(t, "paths") }

func genSym(t *rapid.T) []string {
	return rapid.SliceOfN(rapid.SampledFrom([]string{"Alpha", "Beta", "Gamma", "Delta"}), 0, 4).Draw(t, "syms")
}
