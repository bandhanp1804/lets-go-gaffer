// Package ingest pulls data from the FPL API into storage.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// ErrTooManyFailures is returned when per-player failures exceed Config.MaxFailures.
var ErrTooManyFailures = errors.New("too many failures")

// HistoryFetcher is satisfied by fpl.FPLClient.
type HistoryFetcher interface {
	FetchPlayerHistory(ctx context.Context, playerID int) ([]fpl.PlayerHistory, error)
}

// HistoryStore is satisfied by db.Store.
type HistoryStore interface {
	UpsertHistory(ctx context.Context, rows []fpl.PlayerHistory) error
}

// Config tunes the worker pool.
type Config struct {
	// Workers is the number of players fetched concurrently. Must be >= 1.
	Workers int
	// MaxFailures is how many per-player failures are tolerated. The run aborts
	// as soon as failures exceed it, so a systemic outage (database down, API
	// blocking us) does not grind through every remaining player. 0 aborts on
	// the first failure.
	MaxFailures int
}

// Summary reports what a run did. It is returned even when the run aborts.
type Summary struct {
	Done   int           // players fetched and stored
	Rows   int           // history rows stored
	Failed map[int]error // player id -> why
}

// FailedIDs returns the failed player ids in ascending order.
func (s Summary) FailedIDs() []int {
	ids := make([]int, 0, len(s.Failed))
	for id := range s.Failed {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Histories fetches and stores gameweek history for every id using a bounded
// worker pool.
//
// Shutdown: one producer goroutine feeds a jobs channel and every worker exits
// when it is closed or the pool's context ends. Histories waits for all of them
// before returning, so no goroutine outlives the call.
//
// A fetch or store error is recorded against that player and the run continues.
// An error caused by cancellation is not a player failure and is not recorded.
func Histories(ctx context.Context, f HistoryFetcher, s HistoryStore, ids []int, cfg Config) (Summary, error) {
	if cfg.Workers < 1 {
		return Summary{}, fmt.Errorf("ingest histories: workers must be >= 1, got %d", cfg.Workers)
	}
	sum := Summary{Failed: map[int]error{}}
	if len(ids) == 0 {
		return sum, nil
	}

	// wctx is cancelled by the parent, or by us when the failure budget is blown.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu      sync.Mutex // guards sum and aborted
		aborted bool
		wg      sync.WaitGroup
		jobs    = make(chan int)
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-wctx.Done():
				return
			}
		}
	}()

	record := func(id, rows int, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			sum.Done++
			sum.Rows += rows
			return
		}
		sum.Failed[id] = fmt.Errorf("player %d: %w", id, err)
		if len(sum.Failed) > cfg.MaxFailures && !aborted {
			aborted = true
			cancel()
		}
	}

	process := func(id int) {
		rows, err := f.FetchPlayerHistory(wctx, id)
		if err != nil {
			err = fmt.Errorf("fetch history: %w", err)
		} else if err = s.UpsertHistory(wctx, rows); err != nil {
			err = fmt.Errorf("store history: %w", err)
		}
		if err != nil && wctx.Err() != nil {
			return // killed by cancellation, not this player's fault
		}
		record(id, len(rows), err)
	}

	workers := min(cfg.Workers, len(ids))
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				// A select can pick a ready job even after cancellation, so check.
				if wctx.Err() != nil {
					return
				}
				process(id)
			}
		}()
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return sum, fmt.Errorf("ingest histories: %w", err)
	}
	if aborted {
		return sum, fmt.Errorf("ingest histories: %w: %d failed, budget %d",
			ErrTooManyFailures, len(sum.Failed), cfg.MaxFailures)
	}
	return sum, nil
}
