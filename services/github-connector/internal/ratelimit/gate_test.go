package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-github/v66/github"
	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/testsupport"
)

func TestBucketRefillsContinuouslyAndIsolatesPerInstallation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	budget := NewBudget(3, func() time.Time { return clock })

	require.True(t, budget.TryAcquire(1))
	require.True(t, budget.TryAcquire(1))
	require.True(t, budget.TryAcquire(1))
	require.False(t, budget.TryAcquire(1), "budget of 3 is spent")
	require.True(t, budget.TryAcquire(2), "installations have INDEPENDENT buckets")

	// WHY continuous refill: 1h later the full 3 tokens are back.
	clock = now.Add(time.Hour)
	require.True(t, budget.TryAcquire(1))
}

func TestBucketRetryInSchedulesPendingWrites(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	budget := NewBudget(1, func() time.Time { return clock })
	require.True(t, budget.TryAcquire(7))
	wait := budget.RetryIn(7)
	require.Positive(t, wait)
	require.LessOrEqual(t, wait, time.Hour)
	require.Zero(t, budget.RetryIn(99), "unknown bucket has tokens")
}

func TestGateForced429HonorsRetryAfterThenRetries(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	client := github.NewClient(nil)
	base, _ := parseTestURL(fake.BaseURL + "/")
	client.BaseURL = base

	var waits []time.Duration
	gate := NewGate(NewBudget(10, nil), slog.Default())
	gate.sleep = func(d time.Duration) { waits = append(waits, d) }

	fake.QueueFailure(testsupport.FailureStep{Code: http.StatusTooManyRequests, RetryAfter: "17"})
	attempt := 0
	err := gate.Do(context.Background(), 42, func(ctx context.Context) error {
		attempt++
		_, _, callErr := client.Checks.CreateCheckRun(ctx, "acme", "payments", github.CreateCheckRunOptions{
			Name: "x", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
		return callErr
	})
	require.NoError(t, err, "retry after honoring Retry-After succeeds")
	require.Equal(t, 2, attempt)
	require.Equal(t, 2, fake.Requests(), "one failure + one retry hit the API")
	require.Len(t, waits, 1)
	require.Equal(t, 17*time.Second, waits[0], "Retry-After header wins over backoff")
}

func TestGateJitteredRetryOn5xxAndFailFastOnOtherErrors(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	client := github.NewClient(nil)
	base, _ := parseTestURL(fake.BaseURL + "/")
	client.BaseURL = base

	gate := NewGate(NewBudget(10, nil), slog.Default())
	gate.sleep = func(time.Duration) {}

	call := func(ctx context.Context) error {
		_, _, err := client.Checks.CreateCheckRun(context.Background(), "acme", "payments",
			github.CreateCheckRunOptions{Name: "x", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
		return err
	}
	// Three injected 500s exhaust the bounded attempts (3) and propagate.
	fake.QueueFailure(testsupport.FailureStep{Code: http.StatusInternalServerError})
	fake.QueueFailure(testsupport.FailureStep{Code: http.StatusInternalServerError})
	fake.QueueFailure(testsupport.FailureStep{Code: http.StatusInternalServerError})
	require.Error(t, gate.Do(context.Background(), 42, call), "5xx retries exhaust then propagate")
	require.Equal(t, 3, fake.Requests(), "three bounded attempts on 5xx")

	before := fake.Requests()
	fake.QueueFailure(testsupport.FailureStep{Code: http.StatusUnprocessableEntity})
	require.Error(t, gate.Do(context.Background(), 42, call))
	require.Equal(t, before+1, fake.Requests(), "non-retryable errors fail FAST (no extra attempt)")
}

func TestGateRejectsWithoutCallingWhenBudgetEmpty(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	client := github.NewClient(nil)
	base, _ := parseTestURL(fake.BaseURL + "/")
	client.BaseURL = base

	budget := NewBudget(0, nil)
	gate := NewGate(budget, slog.Default())
	called := false
	err := gate.Do(context.Background(), 42, func(ctx context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, ErrBudgetExhausted)
	require.False(t, called)
	require.Empty(t, fake.Calls())
}

var errSentinel = errors.New("sentinel")

func TestGatePropagatesUntypedErrorsUntouched(t *testing.T) {
	gate := NewGate(NewBudget(5, nil), slog.Default())
	err := gate.Do(context.Background(), 1, func(ctx context.Context) error { return errSentinel })
	require.ErrorIs(t, err, errSentinel)
}
