package httpx_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// These use the real clock with small intervals and assert only lower bounds,
// which scheduler jitter can never violate.

func TestLimiter_SpacesSequentialCalls(t *testing.T) {
	const (
		interval = 20 * time.Millisecond
		n        = 5
	)
	l := httpx.NewLimiter(interval)

	start := time.Now()
	for i := 0; i < n; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}

	// The first call is free, so n calls need at least (n-1) intervals.
	if got, want := time.Since(start), (n-1)*interval; got < want {
		t.Errorf("%d calls took %v, want >= %v", n, got, want)
	}
}

func TestLimiter_WaitHonoursContext(t *testing.T) {
	l := httpx.NewLimiter(time.Hour)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait should be immediate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := l.Wait(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait blocked %v after ctx expired", elapsed)
	}
}

func TestRateLimitTransport_SpacesRequests(t *testing.T) {
	const interval = 20 * time.Millisecond
	rt := &scripted{steps: repeat(3, step{status: 200})}
	tr := httpx.NewRateLimitTransport(rt, httpx.NewLimiter(interval))

	start := time.Now()
	for i := 0; i < 3; i++ {
		resp, err := get(context.Background(), tr)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	if got, want := time.Since(start), 2*interval; got < want {
		t.Errorf("3 requests took %v, want >= %v", got, want)
	}
	if rt.calls != 3 {
		t.Errorf("calls = %d, want 3", rt.calls)
	}
}

func TestLimiter_ConcurrentCallersAreSpaced(t *testing.T) {
	const (
		interval   = 5 * time.Millisecond
		goroutines = 8
		perG       = 3
	)
	l := httpx.NewLimiter(interval)

	start := time.Now()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := l.Wait(context.Background()); err != nil {
					t.Errorf("Wait: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Total slots are shared across goroutines, not per goroutine.
	total := goroutines * perG
	if got, want := time.Since(start), time.Duration(total-1)*interval; got < want {
		t.Errorf("%d concurrent calls took %v, want >= %v", total, got, want)
	}
}

func TestWindowLimiter_LetsNGoAtOnceThenWaitsForTheWindow(t *testing.T) {
	const window = 80 * time.Millisecond
	l := httpx.NewWindowLimiter(3, window)

	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if burst := time.Since(start); burst >= window/2 {
		t.Errorf("the first 3 took %v: they should not wait", burst)
	}

	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := time.Since(start); got < window {
		t.Errorf("the 4th started after %v, want >= %v (the window from the first)", got, window)
	}
}

func TestWindowLimiter_ConcurrentCallersNeverExceedNPerWindow(t *testing.T) {
	const (
		n      = 2
		window = 60 * time.Millisecond
		calls  = 6
	)
	l := httpx.NewWindowLimiter(n, window)
	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Wait(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
		}()
	}
	wg.Wait()

	// In start order, start i and start i+n must be at least a window apart.
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	// Slots are reserved exactly a window apart, but a goroutine can be scheduled a moment after
	// its slot, so the observed gap may fall short by scheduler jitter (callers pad the window).
	const jitter = 5 * time.Millisecond
	for i := 0; i+n < len(starts); i++ {
		if gap := starts[i+n].Sub(starts[i]); gap < window-jitter {
			t.Errorf("starts %d and %d are %v apart, want >= %v", i, i+n, gap, window)
		}
	}
}

func TestWindowLimiter_WaitHonoursContext(t *testing.T) {
	l := httpx.NewWindowLimiter(1, time.Hour)
	_ = l.Wait(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := l.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
}
