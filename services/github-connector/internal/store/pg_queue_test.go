package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/queue"
)

// Compile-time wiring proof: the PG-backed adapter satisfies the pending
// queue seam the drainer consumes.
var _ queue.Store = (*PGPendingQueue)(nil)

func testWrite(repo string) queue.PendingWrite {
	return queue.PendingWrite{
		Key:            "dec_pgq_" + repo + fmt.Sprintf("%016x", time.Now().UnixNano()),
		InstallationID: 42,
		Repo:           "acme/" + repo,
		Op:             queue.OpCreateCheck,
		Payload:        checks.CheckPayload{Name: "Agent Verification Gate"},
	}
}

func TestPGPendingQueueEnqueueDedupeDueDeliver(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	q := NewPendingQueue(st)
	now := time.Now().UTC()

	w := testWrite("alpha")
	require.NoError(t, q.Enqueue(ctx, w))
	// Same §4 idempotency basis ⇒ collapsed, not stacked.
	require.NoError(t, q.Enqueue(ctx, w))

	due, err := q.Due(ctx, now.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, due, 1, "duplicate key collapses to one row")
	require.Equal(t, w.Key, due[0].Key)
	require.NotEmpty(t, due[0].ID, "storage mints the row id")
	require.False(t, due[0].CreatedAt.IsZero())

	// Not-yet-due rows stay invisible to the drainer.
	future := testWrite("beta")
	future.NextAttemptAt = now.Add(time.Hour)
	require.NoError(t, q.Enqueue(ctx, future))
	due, err = q.Due(ctx, now.Add(time.Minute), 10)
	require.NoError(t, err)
	for _, d := range due {
		require.NotEqual(t, future.Key, d.Key, "future write must not be due")
	}

	// MarkDelivered removes the row from the due set permanently.
	require.NoError(t, q.MarkDelivered(ctx, due[0].ID, now))
	due, err = q.Due(ctx, now.Add(time.Minute), 10)
	require.NoError(t, err)
	for _, d := range due {
		require.NotEqual(t, w.Key, d.Key)
	}
}

func TestPGPendingQueueRescheduleBackoff(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	q := NewPendingQueue(st)
	now := time.Now().UTC()

	w := testWrite("gamma")
	w.NextAttemptAt = now
	require.NoError(t, q.Enqueue(ctx, w))
	due, _ := q.Due(ctx, now.Add(time.Minute), 10)
	require.Len(t, due, 1)

	next := now.Add(5 * time.Minute)
	require.NoError(t, q.Reschedule(ctx, due[0].ID, next, 3))
	due, _ = q.Due(ctx, now.Add(time.Minute), 100)
	for _, d := range due {
		require.NotEqual(t, w.Key, d.Key, "rescheduled row leaves the current due window")
	}
	due, _ = q.Due(ctx, now.Add(6*time.Minute), 100)
	var rescheduled *queue.PendingWrite
	for i := range due {
		if due[i].Key == w.Key {
			rescheduled = &due[i]
		}
	}
	require.NotNil(t, rescheduled, "row re-enters the due window after the backoff elapses")
	require.Equal(t, 3, rescheduled.Attempts, "attempt counter persists for backoff")
}

func TestPGPendingQueueUnknownIDOpsAreNoops(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	q := NewPendingQueue(st)
	now := time.Now().UTC()

	require.NoError(t, q.MarkDelivered(ctx, "pw_missing", now), "memory parity: unknown id is a no-op")
	require.NoError(t, q.Reschedule(ctx, "pw_missing", now, 1))
	due, err := q.Due(ctx, now, 0)
	require.NoError(t, err)
	require.Empty(t, due)
}
