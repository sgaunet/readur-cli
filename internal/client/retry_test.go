package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckRetry_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retry, err := CheckRetry(ctx, nil, nil)
	if retry {
		t.Fatalf("should not retry on cancelled context")
	}
	if err == nil {
		t.Fatalf("should surface ctx.Err()")
	}
}

func TestCheckRetry_RetryableStatus(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504, 429, 408} {
		resp := &http.Response{StatusCode: code}
		retry, _ := CheckRetry(context.Background(), resp, nil)
		if !retry {
			t.Fatalf("HTTP %d should retry", code)
		}
	}
}

func TestCheckRetry_NonRetryableStatus(t *testing.T) {
	for _, code := range []int{200, 201, 400, 401, 403, 404, 413, 422} {
		resp := &http.Response{StatusCode: code}
		retry, _ := CheckRetry(context.Background(), resp, nil)
		if retry {
			t.Fatalf("HTTP %d should NOT retry", code)
		}
	}
}

func TestCheckRetry_RetryableErr(t *testing.T) {
	retry, err := CheckRetry(context.Background(), nil, io.EOF)
	if !retry || err != nil {
		t.Fatalf("EOF should retry, got retry=%v err=%v", retry, err)
	}
}

func TestParseRetryAfter_DeltaSeconds(t *testing.T) {
	if d := parseRetryAfter("7", time.Now()); d != 7*time.Second {
		t.Fatalf("got %v, want 7s", d)
	}
	if d := parseRetryAfter("0", time.Now()); d != 0 {
		t.Fatalf("got %v, want 0", d)
	}
	if d := parseRetryAfter("-5", time.Now()); d != 0 {
		t.Fatalf("negative should yield 0, got %v", d)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().UTC().Add(10 * time.Second).Format(http.TimeFormat)
	d := parseRetryAfter(future, time.Now())
	if d < 5*time.Second || d > 15*time.Second {
		t.Fatalf("http-date future ~10s got %v", d)
	}
}

func TestParseRetryAfter_MalformedIsZero(t *testing.T) {
	if d := parseRetryAfter("tomorrow maybe", time.Now()); d != 0 {
		t.Fatalf("malformed should be 0, got %v", d)
	}
	if d := parseRetryAfter("", time.Now()); d != 0 {
		t.Fatalf("empty should be 0, got %v", d)
	}
}

func TestBackoff_429HonorsRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": {"30"}},
	}
	// At attempt 0, exponential would be ~1s ± jitter. Retry-After says 30s.
	d := Backoff(MinBackoff, MaxBackoff, 0, resp)
	if d != 30*time.Second {
		t.Fatalf("expected 30s from Retry-After, got %v", d)
	}
}

func TestBackoff_429SmallRetryAfterUsesBackoff(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": {"0"}},
	}
	d := Backoff(MinBackoff, MaxBackoff, 2, resp)
	// At attempt 2 exponential ≈ 4s ± jitter → 3–4.5s; must exceed 0.
	if d < MinBackoff || d > MaxBackoff {
		t.Fatalf("expected bounded exponential, got %v", d)
	}
}

func TestBackoff_Capped(t *testing.T) {
	for range 10 {
		d := Backoff(MinBackoff, MaxBackoff, 10, &http.Response{StatusCode: 500})
		if d > MaxBackoff+MaxBackoff/4 { // jitter can extend by up to 25%
			t.Fatalf("backoff exceeded cap+jitter: %v", d)
		}
	}
}

// End-to-end: a handler returns 503 three times then 200; the client
// should reach success on the 4th attempt (the last allowed) and not
// beyond.
func TestRetryableClient_ReachesSuccessInBudget(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&n, 1)
		if attempt < 4 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not yet"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewRetryableClient()
	// Tighten wait bounds for test speed.
	c.RetryWaitMin = 1 * time.Millisecond
	c.RetryWaitMax = 10 * time.Millisecond

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.StandardClient().Do(req)
	if err != nil {
		t.Fatalf("final err: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&n); got != 4 {
		t.Fatalf("attempts = %d, want 4 (budget exhausted)", got)
	}
}

// End-to-end: 4 consecutive failures exhaust the budget and surface an
// error. This locks the "exactly 4 attempts" guarantee.
func TestRetryableClient_BudgetExhausted(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewRetryableClient()
	c.RetryWaitMin = 1 * time.Millisecond
	c.RetryWaitMax = 10 * time.Millisecond

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.StandardClient().Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil && resp.StatusCode == 200 {
		t.Fatalf("should not have succeeded")
	}
	if got := atomic.LoadInt32(&n); got != 4 {
		t.Fatalf("attempts = %d, want 4", got)
	}
}

// Verify a 429 with Retry-After actually delays (smoke, not precise).
func TestRetryableClient_429HonorsRetryAfter(t *testing.T) {
	var firstAt, secondAt atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now().UnixMilli()
		if firstAt.Load() == 0 {
			firstAt.Store(now)
			w.Header().Set("Retry-After", "1") // 1 second
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondAt.Store(now)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewRetryableClient()
	// Leave real backoff active so Retry-After gets compared.
	c.RetryWaitMin = 10 * time.Millisecond
	c.RetryWaitMax = 10 * time.Millisecond

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.StandardClient().Do(req)
	if err != nil {
		t.Fatalf("final err: %v", err)
	}
	_ = resp.Body.Close()

	gap := time.Duration(secondAt.Load()-firstAt.Load()) * time.Millisecond
	if gap < 900*time.Millisecond {
		t.Fatalf("Retry-After not honored: gap = %v", gap)
	}
}

