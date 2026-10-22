package httpx

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Limiter hands out one slot per interval. Callers reserve the next free slot
// under a mutex, then wait outside it, so concurrent workers queue fairly.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func NewLimiter(interval time.Duration) *Limiter {
	return &Limiter{interval: interval}
}

// Wait blocks until the caller's slot arrives or ctx ends. A caller that gives
// up still consumes its slot; for a polite API client that is the safe side.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	now := time.Now()
	slot := l.next
	if slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	return sleepCtx(ctx, slot.Sub(now))
}

type rateLimitTransport struct {
	base    http.RoundTripper
	limiter *Limiter
}

// NewRateLimitTransport makes every request wait for a limiter slot. Place it
// inside the retry transport so each retry attempt is limited too.
func NewRateLimitTransport(base http.RoundTripper, l *Limiter) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &rateLimitTransport{base: base, limiter: l}
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}
