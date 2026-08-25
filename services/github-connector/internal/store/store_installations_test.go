package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryInstallationLifecycle(t *testing.T) {
	st := NewMemoryStore(nil)
	ctx := context.Background()

	if err := st.UpsertInstallation(ctx, Installation{ID: 42, AccountLogin: "acme", Permissions: map[string]string{"checks": "write"}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.LinkRepo(ctx, 42, "acme", "payments"); err != nil {
		t.Fatalf("link: %v", err)
	}

	id, err := st.ResolveInstallation(ctx, "acme", "payments")
	if err != nil || id != 42 {
		t.Fatalf("resolve = %d,%v want 42,nil", id, err)
	}
	if _, err := st.ResolveInstallation(ctx, "acme", "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown repo must fail closed with ErrNotFound, got %v", err)
	}
	if err := st.MarkSuspended(ctx, 42, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	statuses, err := st.InstallationStatuses(ctx, 15*time.Minute, time.Now())
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses = %v,%v", statuses, err)
	}
	if !statuses[0].Suspended || statuses[0].Account != "acme" {
		t.Fatalf("suspension/account lost: %+v", statuses[0])
	}
}

// TestMemoryRecordCheckReportRevisionUniqueness pins plan §4.1 semantics:
// one LIVE row per (candidate_id, head_sha); a new decision supersedes the
// prior live row while a known decision replays as ErrDuplicate.
func TestMemoryRecordCheckReportRevisionUniqueness(t *testing.T) {
	st := NewMemoryStore(func() time.Time { return time.Unix(0, 0).UTC() })
	ctx := context.Background()
	first := CheckReport{DecisionID: "dec_1", CandidateID: "cand_1", Repo: "acme/payments", HeadSHA: rev40("a"), Verb: "eligible_for_merge_train", Conclusion: "success"}
	if err := st.RecordCheckReport(ctx, first); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if err := st.RecordCheckReport(ctx, first); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same decision must replay ErrDuplicate, got %v", err)
	}
	second := CheckReport{DecisionID: "dec_2", CandidateID: "cand_1", Repo: "acme/payments", HeadSHA: rev40("a"), Verb: "rejected", Conclusion: "failure"}
	if err := st.RecordCheckReport(ctx, second); err != nil {
		t.Fatalf("new decision same revision: %v", err)
	}
	gotFirst, _ := st.GetCheckReport(ctx, "dec_1")
	if gotFirst.Live {
		t.Fatal("superseded decision must not stay live")
	}
	gotSecond, _ := st.GetCheckReport(ctx, "dec_2")
	if !gotSecond.Live {
		t.Fatal("latest decision for a revision must be live")
	}
}

func rev40(seed string) string {
	out := make([]byte, 0, 40)
	for len(out) < 40 {
		out = append(out, seed...)
	}
	return string(out[:40])
}
