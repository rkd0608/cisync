package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v66/github"
)

// ErrBudgetExhausted reports the local hourly write budget being empty; the
// caller must queue (never drop — plan §4.6).
var ErrBudgetExhausted = errors.New("ratelimit: local write budget exhausted")

// WriteFunc is one GitHub WRITE attempt (create/update check run).
type WriteFunc func(ctx context.Context) error

// Gate wraps write calls with budget acquisition, Retry-After / secondary-
// rate honoring, and bounded jittered retries on 5xx. Sleep is injectable
// for tests; attempts are bounded so a hard-failing API surfaces quickly.
type Gate struct {
	budget   *Budget
	attempts int
	sleep    func(time.Duration)
	rng      *rand.Rand
	logger   *slog.Logger
}

// NewGate builds a gate around the shared per-installation budget.
func NewGate(budget *Budget, logger *slog.Logger) *Gate {
	return &Gate{
		budget:   budget,
		attempts: 3,
		sleep:    time.Sleep,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		logger:   logger,
	}
}

// SetSleeper overrides the wait function (tests inject a recorder instead
// of actually sleeping through Retry-After/backoff waits).
func (g *Gate) SetSleeper(sleep func(time.Duration)) {
	g.sleep = sleep
}

// Do runs fn under the installation's write budget:
//   - budget empty ⇒ ErrBudgetExhausted WITHOUT calling GitHub;
//   - 429/secondary rate ⇒ wait Retry-After (or exp backoff) then retry;
//   - 5xx ⇒ jittered exponential backoff then retry;
//   - other errors and exhausted attempts propagate unchanged.
func (g *Gate) Do(ctx context.Context, installationID int64, fn WriteFunc) error {
	if !g.budget.TryAcquire(installationID) {
		return ErrBudgetExhausted
	}
	var lastErr error
	for attempt := 0; attempt < g.attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		wait, retryable := backoffFor(lastErr, attempt, g.rng)
		if !retryable || attempt == g.attempts-1 {
			return lastErr
		}
		g.logger.Warn("github write retried",
			slog.Int("attempt", attempt+1),
			slog.Duration("wait", wait),
			slog.String("err", lastErr.Error()))
		g.sleep(wait)
	}
	return lastErr
}

// backoffFor classifies a GitHub error into (wait, retryable):
//   - AbuseRateLimitError honors RetryAfter (secondary rate limits);
//   - RateLimitError (primary, 403+remaining:0) uses exp backoff;
//   - plain 429 honors the Retry-After header, else exp backoff;
//   - 5xx uses jittered exp backoff.
//
// WHY the plain-429 branch: go-github v66 only types 403-based rate limit
// errors; a bare 429 surfaces as ErrorResponse and must be classified here.
func backoffFor(err error, attempt int, rng *rand.Rand) (time.Duration, bool) {
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) && abuse.RetryAfter != nil {
		return *abuse.RetryAfter, true
	}
	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		return jitteredBackoff(attempt, rng), true
	}
	var respErr *github.ErrorResponse
	if errors.As(err, &respErr) && respErr.Response != nil {
		code := respErr.Response.StatusCode
		if code == http.StatusTooManyRequests {
			if wait, ok := retryAfterHeader(respErr.Response); ok {
				return wait, true
			}
		}
		if code >= http.StatusInternalServerError {
			return jitteredBackoff(attempt, rng), true
		}
	}
	return 0, false
}

// retryAfterHeader parses a numeric Retry-After header (GitHub always sends
// seconds, never an HTTP-date).
func retryAfterHeader(resp *http.Response) (time.Duration, bool) {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

const backoffBase = 500 * time.Millisecond

// jitteredBackoff: base·2^attempt plus up to 50% full jitter to avoid
// synchronized thundering herds across concurrent publications.
func jitteredBackoff(attempt int, rng *rand.Rand) time.Duration {
	backoff := backoffBase << attempt
	jitter := time.Duration(rng.Int63n(int64(backoff) / 2))
	return backoff + jitter
}
