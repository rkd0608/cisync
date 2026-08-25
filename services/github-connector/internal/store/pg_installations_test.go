package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// pgStore opens the shared dev Postgres (TEST_PG_DSN); the ghconn migrations
// are additive and idempotent so concurrent builders never collide. Unique
// per-test installation ids / candidate ids provide isolation instead of
// scratch schemas (bigint ids and 40-hex shas cannot collide across tests).
func pgStore(t *testing.T) *PGStore {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping PG-backed store test")
	}
	ctx := context.Background()
	st, err := NewPGStore(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestPGInstallationResolutionFailClosed(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	// Unique ids/names isolate against the shared dev Postgres across runs
	// and concurrent builders.
	instID := time.Now().UnixNano()
	repoName := fmt.Sprintf("payments-%d", instID)

	if err := st.UpsertInstallation(ctx, Installation{ID: instID, AccountLogin: "acme", Permissions: map[string]string{"checks": "write", "metadata": "read"}}); err != nil {
		t.Fatalf("upsert installation: %v", err)
	}
	if _, err := st.ResolveInstallation(ctx, "acme", "ghost-repo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlinked repo must fail closed ErrNotFound, got %v", err)
	}
	if err := st.LinkRepo(ctx, instID, "acme", repoName); err != nil {
		t.Fatalf("link repo: %v", err)
	}
	id, err := st.ResolveInstallation(ctx, "acme", repoName)
	if err != nil || id != instID {
		t.Fatalf("resolve = %d,%v want %d,nil", id, err, instID)
	}
	if err := st.MarkSuspended(ctx, instID, true); err != nil {
		t.Fatalf("mark suspended: %v", err)
	}
	statuses, err := st.InstallationStatuses(ctx, 15*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}
	var found *InstallationStatus
	for i := range statuses {
		if statuses[i].InstallationID == instID {
			found = &statuses[i]
		}
	}
	if found == nil {
		t.Fatalf("installation %d missing from status projection", instID)
	}
	if !found.Suspended || found.Account != "acme" {
		t.Fatalf("suspension/account not projected: %+v", found)
	}
	if len(found.Repos) != 1 || found.Repos[0].Name != repoName || found.Repos[0].WebhookState != "pending" {
		t.Fatalf("repo status wrong: %+v", found.Repos)
	}
	if found.Permissions["checks"] != "write" {
		t.Fatalf("permissions snapshot lost: %+v", found.Permissions)
	}
}

// TestPGRecordCheckReportOneLivePerRevision verifies the partial unique index
// contract end-to-end against real Postgres. WHY the dedicated installation:
// InstallationStatuses joins ghconn.installation_repos, so the observation
// assertion needs a self-created link instead of relying on leftover rows
// from other tests (W5 integration fix — clean-database reproducible).
func TestPGRecordCheckReportOneLivePerRevision(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	revision := time.Now().UTC().Format("20060102150405")
	headSHA := rev40(revision)[:40]
	candidate := "cand_" + revision
	instID := time.Now().UnixNano()
	repoBare := fmt.Sprintf("payments-%d", instID)
	repoFull := "acme/" + repoBare

	if err := st.UpsertInstallation(ctx, Installation{ID: instID, AccountLogin: "acme"}); err != nil {
		t.Fatalf("upsert installation: %v", err)
	}
	if err := st.LinkRepo(ctx, instID, "acme", repoBare); err != nil {
		t.Fatalf("link repo: %v", err)
	}

	first := CheckReport{DecisionID: "dec_pg1_" + revision, CandidateID: candidate, Repo: repoFull, HeadSHA: headSHA, Verb: "eligible_for_merge_train", Conclusion: "success"}
	if err := st.RecordCheckReport(ctx, first); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if err := st.RecordCheckReport(ctx, first); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("replay must be ErrDuplicate, got %v", err)
	}
	second := CheckReport{DecisionID: "dec_pg2_" + revision, CandidateID: candidate, Repo: repoFull, HeadSHA: headSHA, Verb: "rejected", Conclusion: "failure"}
	if err := st.RecordCheckReport(ctx, second); err != nil {
		t.Fatalf("second decision same revision must update in place: %v", err)
	}
	statuses, err := st.InstallationStatuses(ctx, 15*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}
	var observed bool
	for _, inst := range statuses {
		if inst.InstallationID != instID {
			continue
		}
		for _, r := range inst.Repos {
			if r.Name == repoBare && r.WebhookState == "receiving" && r.LastDeliverySeq >= 2 {
				observed = true
			}
		}
	}
	if !observed {
		t.Fatal("recorded reports must feed the delivery observation for webhook_state=receiving")
	}
}
