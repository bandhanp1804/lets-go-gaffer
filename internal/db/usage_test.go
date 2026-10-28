package db_test

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStore_TakeUsageStopsAtTheLimitPerUserKindAndDay(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 28, 23, 0, 0, 0, time.UTC)

	for i := 1; i <= 3; i++ {
		n, ok, err := s.TakeUsage(ctx, "alice", "ask", day, 3)
		if err != nil || !ok || n != i {
			t.Fatalf("use %d: n=%d ok=%v err=%v", i, n, ok, err)
		}
	}
	if n, ok, _ := s.TakeUsage(ctx, "alice", "ask", day, 3); ok || n != 3 {
		t.Errorf("past the limit: n=%d ok=%v", n, ok)
	}
	if _, ok, _ := s.TakeUsage(ctx, "alice", "search", day, 3); !ok {
		t.Error("another kind has its own count")
	}
	if _, ok, _ := s.TakeUsage(ctx, "bob", "ask", day, 3); !ok {
		t.Error("another user has their own count")
	}
	if _, ok, _ := s.TakeUsage(ctx, "alice", "ask", day.Add(2*time.Hour), 3); !ok {
		t.Error("a new day starts again")
	}

	got, err := s.UsageOn(ctx, "alice", day)
	if err != nil || got["ask"] != 3 || got["search"] != 1 {
		t.Errorf("usage = %v (%v)", got, err)
	}
}

func TestStore_TakeUsageUnderConcurrencyNeverGoesOver(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := s.TakeUsage(ctx, "alice", "ask", day, 5); err == nil && ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Errorf("%d of 20 concurrent uses allowed, want exactly 5", allowed)
	}
}

func TestStore_PruneUsageDropsOnlyOlderDays(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	_, _, _ = s.TakeUsage(ctx, "alice", "ask", old, 5)
	_, _, _ = s.TakeUsage(ctx, "alice", "ask", recent, 5)

	n, err := s.PruneUsage(ctx, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC))

	if err != nil || n != 1 {
		t.Fatalf("pruned %d (%v), want 1", n, err)
	}
	if got, _ := s.UsageOn(ctx, "alice", recent); got["ask"] != 1 {
		t.Errorf("the recent day must stay: %v", got)
	}
}
