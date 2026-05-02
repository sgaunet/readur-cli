package client

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// Retry constants enforce the budget from spec.md Q2:
//   - 4 total attempts (1 initial + 3 retries)
//   - exponential backoff with jitter, min 1 s, max 10 s between attempts
//   - on 429, wait max(exponential_backoff, Retry-After)
const (
	MaxRetries = 3
	MinBackoff = 1 * time.Second
	MaxBackoff = 10 * time.Second
)

// jitterSpreadDivisor produces the ±25% spread used by the
// exponential backoff: jitter ∈ [0, d/jitterSpreadDivisor), shifted so
// the resulting wait sits in [0.75d, 1.25d).
const jitterSpreadDivisor = 2

// CheckRetry is the retryablehttp.CheckRetry implementation for this
// client. It returns (shouldRetry, err) where a non-nil err halts
// the request immediately.
//
// We retry when (a) the request's context is still alive, (b) the
// underlying error is in the retryable transport class, or (c) the
// HTTP status is in the retryable class (5xx, 408, 429).
func CheckRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// Do not retry if the caller cancelled; surface the context error.
	if ctx.Err() != nil {
		return false, fmt.Errorf("request cancelled: %w", ctx.Err())
	}
	if err != nil {
		return IsRetryableErr(err), nil
	}
	if resp == nil {
		return false, nil
	}
	return IsRetryableStatus(resp.StatusCode), nil
}

// Backoff selects the duration to wait between attempts. It honors
// Retry-After on 429 (and, by extension, on 503), taking the maximum
// of the exponential-backoff-with-jitter value and the server-hinted
// delay. Jitter is ±25% of the exponential value.
func Backoff(minWait, maxWait time.Duration, attemptNumber int, resp *http.Response) time.Duration {
	exp := exponentialBackoff(minWait, maxWait, attemptNumber)

	if resp != nil && (resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusServiceUnavailable) {
		if hint := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); hint > 0 {
			if hint > exp {
				return hint
			}
		}
	}
	return exp
}

// exponentialBackoff implements 1s, 2s, 4s, 8s capped at MaxBackoff,
// plus ±25% jitter. attemptNumber is 0-indexed for the first retry.
func exponentialBackoff(minWait, maxWait time.Duration, attemptNumber int) time.Duration {
	if attemptNumber < 0 {
		attemptNumber = 0
	}
	// exponential: min * 2^attemptNumber
	d := minWait << attemptNumber
	if d <= 0 || d > maxWait {
		d = maxWait
	}
	// jitter ±25%
	// #nosec G404 — jitter does not require cryptographic randomness
	jitter := time.Duration(rand.Int64N(int64(d) / jitterSpreadDivisor))
	return d - d/4 + jitter
}

// parseRetryAfter parses the Retry-After header per RFC 7231 §7.1.3:
// either a non-negative integer number of seconds or an HTTP-date.
// Returns 0 if the header is absent or malformed.
func parseRetryAfter(h string, now time.Time) time.Duration {
	if h == "" {
		return 0
	}
	secs, parseIntErr := strconv.ParseInt(h, 10, 64)
	if parseIntErr == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	t, parseTimeErr := http.ParseTime(h)
	if parseTimeErr == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
		_ = now // kept for future deterministic testing support
	}
	return 0
}

// NewRetryableClient returns a *retryablehttp.Client wired with the
// project's CheckRetry + Backoff + attempt budget. The returned client
// has Logger disabled by default (the CLI does its own structured
// logging); callers may override .Logger if needed.
func NewRetryableClient() *retryablehttp.Client {
	c := retryablehttp.NewClient()
	c.RetryMax = MaxRetries
	c.RetryWaitMin = MinBackoff
	c.RetryWaitMax = MaxBackoff
	c.CheckRetry = CheckRetry
	c.Backoff = Backoff
	c.Logger = nil
	return c
}
