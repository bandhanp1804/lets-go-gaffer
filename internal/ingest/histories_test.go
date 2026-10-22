package ingest_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
)

var (
	errFetch = errors.New("fetch failed")
	errStore = errors.New("store failed")
)

type fetchFunc func(ctx context.Context, id int) ([]fpl.PlayerHistory, error)

// twoRows is the default behaviour: every player has two history rows.
func twoRows(_ context.Context, id int) ([]fpl.PlayerHistory, error) {
	return []fpl.PlayerHistory{{PlayerID: id, GameweekID: 1, Fixture: 1}, {PlayerID: id, GameweekID: 2, Fixture: 2}}, nil
}

// fakeFetcher records calls and how many ran at the same moment.
type fakeFetcher struct {
	fn fetchFunc

	mu          sync.Mutex
	calls       map[int]int
	inflight    int
	maxInflight int
}

func newFetcher(fn fetchFunc) *fakeFetcher {
	if fn == nil {
		fn = twoRows
	}
	return &fakeFetcher{fn: fn, calls: map[int]int{}}
}

func (f *fakeFetcher) FetchPlayerHistory(ctx context.Context, id int) ([]fpl.PlayerHistory, error) {
	f.mu.Lock()
	f.calls[id]++
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	return f.fn(ctx, id)
}

func (f *fakeFetcher) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// fakeStore records what was stored per player; failFor makes chosen players fail.
type fakeStore struct {
	failFor map[int]bool

	mu     sync.Mutex
	stored map[int][]fpl.PlayerHistory
}

func newStore(failFor ...int) *fakeStore {
	s := &fakeStore{failFor: map[int]bool{}, stored: map[int][]fpl.PlayerHistory{}}
	for _, id := range failFor {
		s.failFor[id] = true
	}
	return s
}

func (s *fakeStore) UpsertHistory(_ context.Context, rows []fpl.PlayerHistory) error {
	if len(rows) == 0 {
		return nil
	}
	id := rows[0].PlayerID
	if s.failFor[id] {
		return errStore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stored[id] = append(s.stored[id], rows...)
	return nil
}

func idRange(n int) []int {
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i + 1
	}
	return ids
}

func TestHistories_FetchesAndStoresEveryPlayerOnce(t *testing.T) {
	f, s := newFetcher(nil), newStore()
	ids := idRange(50)

	sum, err := ingest.Histories(context.Background(), f, s, ids, ingest.Config{Workers: 4, MaxFailures: 0})

	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if f.calls[id] != 1 {
			t.Errorf("player %d fetched %d times, want 1", id, f.calls[id])
		}
		if len(s.stored[id]) != 2 {
			t.Errorf("player %d stored %d rows, want 2", id, len(s.stored[id]))
		}
	}
	if sum.Done != 50 || sum.Rows != 100 || len(sum.Failed) != 0 {
		t.Errorf("summary = %+v, want Done 50, Rows 100, no failures", sum)
	}
}

func TestHistories_RunsWorkersInParallelButNoMoreThanConfigured(t *testing.T) {
	const workers = 4
	// Each call waits until `workers` calls are in flight together. If the pool
	// were serial this would time out, so passing proves real parallelism.
	var arrived atomic.Int32
	release := make(chan struct{})
	f := newFetcher(func(ctx context.Context, id int) ([]fpl.PlayerHistory, error) {
		if arrived.Add(1) == workers {
			close(release)
		}
		select {
		case <-release:
			return twoRows(ctx, id)
		case <-time.After(2 * time.Second):
			return nil, errors.New("workers are not running in parallel")
		}
	})

	sum, err := ingest.Histories(context.Background(), f, newStore(), idRange(20), ingest.Config{Workers: workers, MaxFailures: 100})

	if err != nil || len(sum.Failed) != 0 {
		t.Fatalf("err = %v, failed = %v; want none", err, sum.Failed)
	}
	if f.maxInflight > workers {
		t.Errorf("max in flight = %d, want <= %d", f.maxInflight, workers)
	}
	if f.maxInflight != workers {
		t.Errorf("max in flight = %d, want exactly %d", f.maxInflight, workers)
	}
}

func TestHistories_ToleratesFailuresWithinBudget(t *testing.T) {
	f := newFetcher(func(ctx context.Context, id int) ([]fpl.PlayerHistory, error) {
		if id == 2 {
			return nil, errFetch
		}
		return twoRows(ctx, id)
	})
	s := newStore(5) // player 5 fetches fine but fails to store

	sum, err := ingest.Histories(context.Background(), f, s, idRange(10), ingest.Config{Workers: 3, MaxFailures: 5})

	if err != nil {
		t.Fatalf("err = %v, want nil: 2 failures is within the budget of 5", err)
	}
	if sum.Done != 8 {
		t.Errorf("Done = %d, want 8", sum.Done)
	}
	if got := sum.FailedIDs(); len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Fatalf("FailedIDs = %v, want [2 5] (sorted)", got)
	}
	if !errors.Is(sum.Failed[2], errFetch) || !errors.Is(sum.Failed[5], errStore) {
		t.Errorf("causes lost: %v / %v", sum.Failed[2], sum.Failed[5])
	}
	if len(s.stored[2]) != 0 || len(s.stored[5]) != 0 {
		t.Error("failed players must not leave stored rows")
	}
}

func TestHistories_AbortsOnceFailureBudgetIsExceeded(t *testing.T) {
	const (
		workers     = 2
		maxFailures = 2
	)
	f := newFetcher(func(context.Context, int) ([]fpl.PlayerHistory, error) { return nil, errFetch })

	sum, err := ingest.Histories(context.Background(), f, newStore(), idRange(100), ingest.Config{Workers: workers, MaxFailures: maxFailures})

	if !errors.Is(err, ingest.ErrTooManyFailures) {
		t.Fatalf("err = %v, want ErrTooManyFailures", err)
	}
	// Abort when failures exceed the budget (the 3rd), plus at most one extra
	// job already picked up per worker.
	if limit := maxFailures + 1 + workers; f.totalCalls() > limit {
		t.Errorf("made %d fetches, want <= %d (the pool should stop early)", f.totalCalls(), limit)
	}
	if len(sum.Failed) <= maxFailures {
		t.Errorf("recorded %d failures, want > %d", len(sum.Failed), maxFailures)
	}
}

func TestHistories_ContextCancelStopsWorkersWithoutCountingFailures(t *testing.T) {
	const workers = 3
	f := newFetcher(func(ctx context.Context, _ int) ([]fpl.PlayerHistory, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	sum, err := ingest.Histories(ctx, f, newStore(), idRange(100), ingest.Config{Workers: workers, MaxFailures: 1000})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to stop after cancellation", elapsed)
	}
	if f.totalCalls() > workers {
		t.Errorf("fetches = %d, want <= %d: no new work after cancel", f.totalCalls(), workers)
	}
	if len(sum.Failed) != 0 {
		t.Errorf("failed = %v: cancellation is not a per-player failure", sum.Failed)
	}
}

func TestHistories_LeavesNoGoroutinesBehind(t *testing.T) {
	before := runtime.NumGoroutine()

	// Exercise the abort path, the one most likely to leak a producer or worker.
	f := newFetcher(func(context.Context, int) ([]fpl.PlayerHistory, error) { return nil, errFetch })
	_, _ = ingest.Histories(context.Background(), f, newStore(), idRange(200), ingest.Config{Workers: 8, MaxFailures: 1})

	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines: %d before, %d after; the pool leaked", before, after)
	}
}

func TestHistories_EdgeCases(t *testing.T) {
	t.Run("no ids is a no-op", func(t *testing.T) {
		f := newFetcher(nil)
		sum, err := ingest.Histories(context.Background(), f, newStore(), nil, ingest.Config{Workers: 4})
		if err != nil || sum.Done != 0 || f.totalCalls() != 0 {
			t.Errorf("sum = %+v, err = %v, calls = %d; want all zero", sum, err, f.totalCalls())
		}
	})
	t.Run("player with no history still counts as done", func(t *testing.T) {
		f := newFetcher(func(context.Context, int) ([]fpl.PlayerHistory, error) { return nil, nil })
		sum, err := ingest.Histories(context.Background(), f, newStore(), []int{1}, ingest.Config{Workers: 1})
		if err != nil || sum.Done != 1 || sum.Rows != 0 {
			t.Errorf("sum = %+v, err = %v; want Done 1, Rows 0", sum, err)
		}
	})
	t.Run("zero workers is rejected", func(t *testing.T) {
		_, err := ingest.Histories(context.Background(), newFetcher(nil), newStore(), idRange(3), ingest.Config{Workers: 0})
		if err == nil {
			t.Error("want an error for Workers < 1")
		}
	})
	t.Run("more workers than ids", func(t *testing.T) {
		f := newFetcher(nil)
		sum, err := ingest.Histories(context.Background(), f, newStore(), idRange(2), ingest.Config{Workers: 16})
		if err != nil || sum.Done != 2 {
			t.Errorf("sum = %+v, err = %v; want Done 2", sum, err)
		}
	})
}

// Run under -race: many workers hammering the shared summary.
func TestHistories_RaceStress(t *testing.T) {
	f := newFetcher(func(ctx context.Context, id int) ([]fpl.PlayerHistory, error) {
		if id%7 == 0 {
			return nil, errFetch
		}
		return twoRows(ctx, id)
	})

	sum, err := ingest.Histories(context.Background(), f, newStore(), idRange(500), ingest.Config{Workers: 16, MaxFailures: 500})

	if err != nil {
		t.Fatal(err)
	}
	if want := 500 - 500/7; sum.Done != want {
		t.Errorf("Done = %d, want %d", sum.Done, want)
	}
	if len(sum.Failed) != 500/7 {
		t.Errorf("failed = %d, want %d", len(sum.Failed), 500/7)
	}
}
