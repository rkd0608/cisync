package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBucketBasicAcquireAndExhaustion(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	b := NewBucket(2, func() time.Time { return clock })
	require.True(t, b.TryAcquire())
	require.True(t, b.TryAcquire())
	require.False(t, b.TryAcquire())
	require.Positive(t, b.RetryIn())
}

func TestBucketPartialRefillProportionalToElapsed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	b := NewBucket(1, func() time.Time { return clock }) // refill 1 token/hour
	require.True(t, b.TryAcquire())
	require.False(t, b.TryAcquire())

	// WHY half-hour probe: proves the refill is CONTINUOUS (0.5 tokens
	// accrued is not enough), not a step function at the hour boundary.
	clock = now.Add(30 * time.Minute)
	require.False(t, b.TryAcquire())
	require.Equal(t, 30*time.Minute, b.RetryIn().Round(time.Minute))

	clock = now.Add(75 * time.Minute)
	require.True(t, b.TryAcquire())
}
