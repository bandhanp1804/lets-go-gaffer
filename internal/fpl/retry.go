package fpl

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// MaxRetryAfter caps how long we will obey a server's Retry-After header.
const MaxRetryAfter = 30 * time.Minute

// RetryConfig controls NewRetryTransport.
type RetryConfig struct {
	// MaxAttempts is the total number of tries, including the first.
	MaxAttempts int
	// BaseDelay is the backoff ceiling before the first retry; it doubles each retry.
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff (not Retry-After; see MaxRetryAfter).
	MaxDelay time.Duration
	// Sleep waits for d or until ctx ends. Nil uses a real timer; tests inject a fake.
	Sleep func(ctx context.Context, d time.Duration) error
}

type retryTransport struct {
	base http.RoundTripper
	cfg  RetryConfig
}

// NewRetryTransport retries network errors, 429, and 5xx responses with
// exponential backoff and full jitter, honouring Retry-After (in seconds).
// Other statuses are returned untouched. After the final attempt it returns a
// *RetryError. It only supports requests without a body (all FPL calls are GETs).
func NewRetryTransport(base http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	return &retryTransport{base: base, cfg: cfg}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	var lastErr error

	for attempt := 0; attempt < t.cfg.MaxAttempts; attempt++ {
		var retryAfter time.Duration

		resp, err := t.base.RoundTrip(req)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, err // cancelled, so retrying is pointless
			}
			lastErr = err
		case retryable(resp.StatusCode):
			retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
			lastErr = &StatusError{StatusCode: resp.StatusCode}
			drainAndClose(resp.Body) // free the connection before we back off
		default:
			return resp, nil
		}

		if attempt == t.cfg.MaxAttempts-1 {
			break // no sleep after the final attempt
		}

		delay := t.backoff(attempt)
		if retryAfter > 0 {
			delay = max(delay, retryAfter) // server instructions win over our cap
		}
		if err := t.cfg.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, &RetryError{Attempts: t.cfg.MaxAttempts, Err: lastErr}
}

// backoff returns a random delay in [0, min(MaxDelay, BaseDelay*2^attempt)].
// Full jitter spreads out concurrent workers that failed at the same moment.
func (t *retryTransport) backoff(attempt int) time.Duration {
	ceiling := t.cfg.MaxDelay
	if attempt < 32 { // beyond this the shift could overflow
		ceiling = min(t.cfg.BaseDelay<<attempt, t.cfg.MaxDelay)
	}
	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling + 1)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// parseRetryAfter understands the delta-seconds form only. Anything else is 0.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	if secs >= int(MaxRetryAfter/time.Second) {
		return MaxRetryAfter
	}
	return time.Duration(secs) * time.Second
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
	_ = body.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
