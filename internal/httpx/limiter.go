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

// WindowLimiter lets at most n calls start in any rolling window. Unlike Limiter
// it does not space calls out: the first n go at once, which suits an API whose
// limit is "n requests per minute" and an interactive caller who should not wait
// when the budget has room. Slots are reserved under a mutex, like Limiter's.
type WindowLimiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	starts []time.Time // the last n reserved start times, oldest first
}

func NewWindowLimiter(n int, window time.Duration) *WindowLimiter {
	return &WindowLimiter{n: max(n, 1), window: window}
}

// Wait blocks until starting now keeps at most n starts in the window, or ctx ends.
// A caller that gives up still consumes its slot, as with Limiter.
func (l *WindowLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	now := time.Now()
	slot := now
	if len(l.starts) == l.n {
		if free := l.starts[0].Add(l.window); free.After(slot) {
			slot = free
		}
		l.starts = l.starts[1:]
	}
	l.starts = append(l.starts, slot)
	l.mu.Unlock()

	return sleepCtx(ctx, slot.Sub(now))
}

// Waiter is a limiter: Limiter or WindowLimiter.
type Waiter interface {
	Wait(ctx context.Context) error
}

type rateLimitTransport struct {
	base    http.RoundTripper
	limiter Waiter
}

// NewRateLimitTransport makes every request wait for a limiter slot. Place it
// inside the retry transport so each retry attempt is limited too.
func NewRateLimitTransport(base http.RoundTripper, l Waiter) http.RoundTripper {
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
