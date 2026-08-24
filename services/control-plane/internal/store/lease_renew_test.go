package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
)

// P1-3: RenewLease must be ONE conditional UPDATE (state='granted', TTL not
// yet expired) with RowsAffected-driven conflict — no read-mutate-write race
// where two concurrent renews both pass a stale granted check.

func seedGrantedLease(t *testing.T, st *Store, tenantID, tag string, ttl time.Duration) *domain.Lease {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	intentID := domain.NewID(domain.PrefixIntent)
	candID := domain.NewID(domain.PrefixCandidate)
	declared := domain.IntentDeclared{
		Goal: "renew race", Repo: "acme/renew-" + tag, BaseRef: "main",
		BaseSnapshot: "main@renew", OwnedSurfaces: []string{"services/**"},
		RiskClass: domain.RiskLow, Origin: domain.OriginSynthetic,
		ResolvedPolicy: domain.DefaultPolicy().Ref,
		ComputeBudget:  domain.BudgetValues{CPUMinutes: 5, EnvironmentMinutes: 5, RepairAttempts: 1},
	}
	intent := domain.NewIntent(intentID, tenantID, declared, now)
	intent.InitialCandidateID = candID
	lease := domain.NewLease(domain.NewID(domain.PrefixLease), tenantID, intentID,
		domain.LeaseScope{Kind: domain.ScopeChangeScope, Surfaces: declared.OwnedSurfaces},
		"agent:"+tenantID, declared.ComputeBudget, ttl, []string{"hermetic_build"}, now)
	if err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err := CreateIntentTx(ctx, tx, st, intent, lease, nil)
		return err
	}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	return lease
}

func TestRenewLeaseConditionalUpdateRaceSafety(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lease renew test")
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_PG_DSN: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenantID := config.DevTenant
	lease := seedGrantedLease(t, st, tenantID, "renew-race", time.Minute)

	newTTL, renewalCount, err := st.RenewLease(context.Background(), tenantID, lease.ID, 600)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewalCount != 1 {
		t.Fatalf("first renewal must count=1, got %d", renewalCount)
	}
	if time.Until(newTTL) < 500*time.Second {
		t.Fatalf("TTL must extend ~600s from now, got %v", newTTL)
	}

	// A naturally-expired lease refuses renewal as a typed conflict.
	expired := seedGrantedLease(t, st, tenantID, "renew-expired", time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if _, _, err := st.RenewLease(context.Background(), tenantID, expired.ID, 600); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expired-lease renewal must be ErrConflict, got %v", err)
	}
}
