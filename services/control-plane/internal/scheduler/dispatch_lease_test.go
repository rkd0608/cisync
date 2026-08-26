package scheduler

import (
	"context"
	"os"
	"testing"

	"cisync.dev/cisync/control-plane/internal/config"
	"cisync.dev/cisync/control-plane/internal/joblease"
)

// P0-1 / B2 / I-04: every dispatched run carries an Ed25519-signed job-lease
// token in the enqueue payload, bound to run_id/attempt/fence with aud
// "cisync-fleet" and a TTL within the 60-minute cap. The fleet rejects any
// mutation without such a credential, so dispatch MUST mint one.

func TestDispatchMintsVerifiableJobLeaseToken(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping scheduler PG test")
	}
	engine, st, cleanup := pgScheduler(t)
	defer cleanup()

	fleet := &fakeFleet{}
	engine.fleet = fleet
	signer, err := joblease.NewSignerForTesting()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	engine.leaseSigner = signer

	seeded := seedValidationCandidate(t, st, config.DevTenant, "lease-mint")
	run, err := st.GetRunByID(context.Background(), seeded.runIDs[0])
	if err != nil {
		t.Fatalf("load run: %v", err)
	}

	if _, err := engine.dispatchOne(context.Background(), run.ID, BudgetReservation{}, DefaultPolicySource()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(fleet.enqueued) != 1 {
		t.Fatalf("dispatch must enqueue exactly one job, got %d", len(fleet.enqueued))
	}
	token := fleet.enqueued[0].LeaseToken
	if token == "" {
		t.Fatal("enqueue payload must carry the job-lease token")
	}
	publicPEM := signer.PublicPEM()
	verifier, err := joblease.NewVerifierFromPublicPEM(publicPEM)
	if err != nil {
		t.Fatalf("verifier from public pem: %v", err)
	}
	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("minted lease must verify against the public key: %v", err)
	}
	if claims.RunID != run.ID || claims.Attempt != run.Attempt || claims.FenceToken != 1 {
		t.Fatalf("claims must bind run/attempt/fence: %+v (want fence 1)", claims)
	}
	if claims.Repo != run.JobSpec.Repo || claims.Tier != run.Tier {
		t.Fatalf("claims must bind repo/tier: %+v", claims)
	}
	if claims.Audience != "cisync-fleet" || claims.ID != joblease.JTIBuilds(run.ID, run.Attempt, 1) {
		t.Fatalf("audience/jti wrong: %+v", claims)
	}
}
