package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/checks"
	"cisync.dev/cisync/github-connector/internal/ratelimit"
)

func testPayload(key string) checks.CheckPayload {
	return checks.CheckPayload{Name: checks.CheckName, HeadSHA: "aaaa", Status: "queued", ExternalID: key, Summary: "s"}
}

func TestEnqueueDedupesByKeyAndDueRespectsNextAttempt(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	store := NewMemoryStore(func() time.Time { return clock })
	ctx := context.Background()

	require.NoError(t, store.Enqueue(ctx, PendingWrite{Key: "cand_01J:queued", InstallationID: 7, Repo: "acme/payments", Op: OpCreateCheck, Payload: testPayload("cand_01J")}))
	// Redelivery of the same §4 basis collapses.
	require.NoError(t, store.Enqueue(ctx, PendingWrite{Key: "cand_01J:queued", InstallationID: 7, Repo: "acme/payments", Op: OpCreateCheck, Payload: testPayload("cand_01J")}))

	due, err := store.Due(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)

	// Rescheduled past a horizon is invisible until it comes due again.
	require.NoError(t, store.Reschedule(ctx, due[0].ID, now.Add(2*time.Hour), 1))
	due, err = store.Due(ctx, now.Add(time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, due)
	due, err = store.Due(ctx, now.Add(3*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
}

func TestDrainerDeliversThenMarksDelivered(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore(func() time.Time { return time.Unix(1_800_000_000, 0) })
	budget := ratelimit.NewBudget(10, func() time.Time { return now })
	gate := ratelimit.NewGate(budget, testLogger())
	gate.SetSleeper(func(time.Duration) {})

	delivered := []PendingWrite{}
	drainer := NewDrainer(store, gate, budget, func(ctx context.Context, w PendingWrite) error {
		delivered = append(delivered, w)
		return nil
	}, time.Minute, func() time.Time { return now }, testLogger(), nil)

	w := PendingWrite{Key: "k1", InstallationID: 3, Repo: "acme/payments", Op: OpCreateCheck, Payload: testPayload("k1")}
	require.NoError(t, store.Enqueue(context.Background(), w))
	drainer.tick(context.Background())

	require.Len(t, delivered, 1)
	due, err := store.Due(context.Background(), now.Add(time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, due, "delivered write left the queue")
}

func TestDrainerBudgetExhaustionReschedulesNotDelivers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore(func() time.Time { return now })
	budget := ratelimit.NewBudget(0, func() time.Time { return now }) // always empty
	gate := ratelimit.NewGate(budget, testLogger())

	delivered := 0
	drainer := NewDrainer(store, gate, budget, func(ctx context.Context, w PendingWrite) error {
		delivered++
		return nil
	}, time.Minute, func() time.Time { return now }, testLogger(), nil)

	require.NoError(t, store.Enqueue(context.Background(), PendingWrite{Key: "k", InstallationID: 5, Repo: "acme/payments", Op: OpCreateCheck, Payload: testPayload("k")}))
	drainer.tick(context.Background())
	require.Zero(t, delivered, "exhausted budget must NOT attempt delivery")

	due, err := store.Due(context.Background(), now.Add(2*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, due, 1, "write rescheduled for later — never dropped")
}

func TestDrainerDeliveryFailureBacksOffWithAttemptCap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	store := NewMemoryStore(func() time.Time { return clock })
	budget := ratelimit.NewBudget(10, func() time.Time { return clock })
	gate := ratelimit.NewGate(budget, testLogger())
	gate.SetSleeper(func(time.Duration) {})

	drainer := NewDrainer(store, gate, budget, func(ctx context.Context, w PendingWrite) error {
		return errors.New("github down")
	}, time.Minute, func() time.Time { return clock }, testLogger(), nil)

	require.NoError(t, store.Enqueue(context.Background(), PendingWrite{Key: "k", InstallationID: 5, Repo: "acme/payments", Op: OpUpdateCheck, CheckRunID: 9, Payload: testPayload("k")}))
	// WHY clock advance per tick: backoff hides the write until its
	// next_attempt_at; a frozen clock would only ever deliver once.
	for range maxAttempts + 2 {
		clock = clock.Add(time.Hour)
		drainer.tick(context.Background())
	}
	due, err := store.Due(context.Background(), clock.Add(backoffCap*10), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.GreaterOrEqual(t, due[0].Attempts, maxAttempts, "attempts tracked; write retained")
}
