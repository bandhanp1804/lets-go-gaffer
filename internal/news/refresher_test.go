package news

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefresher_StartRunsOneRefreshAtATime(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	r := NewRefresher(context.Background(), func(context.Context) (Summary, error) {
		calls.Add(1)
		<-release
		return Summary{Fetched: 10, New: 3, SourceErrors: map[string]error{"talksport": errors.New("web page")}}, nil
	})

	first, second := r.Start(), r.Start()
	if !first || second {
		t.Fatalf("Start = %v then %v, want true then false while one is running", first, second)
	}
	if !r.Status().Running {
		t.Error("status should say a refresh is running")
	}

	close(release)
	r.Wait()

	st := r.Status()
	if st.Running || st.LastNew != 3 || st.LastFetched != 10 || st.LastError != "" || st.LastFinished == "" || len(st.LastFailedFeeds) != 1 || st.LastFailedFeeds[0] != "talksport" {
		t.Errorf("status after = %+v", st)
	}
	if calls.Load() != 1 {
		t.Errorf("run called %d times, want 1", calls.Load())
	}
	if !r.Start() { // free again once it finished
		t.Error("Start after the refresh ended should begin another")
	}
	r.Wait()
}

func TestRefresher_ReportsAFailureThenClearsItOnSuccess(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	r := NewRefresher(context.Background(), func(context.Context) (Summary, error) {
		if fail.Load() {
			return Summary{}, errors.New("every source failed")
		}
		return Summary{New: 1}, nil
	})

	r.Start()
	r.Wait()
	if got := r.Status().LastError; got != "every source failed" {
		t.Errorf("LastError = %q", got)
	}

	fail.Store(false)
	r.Start()
	r.Wait()
	if st := r.Status(); st.LastError != "" || st.LastNew != 1 {
		t.Errorf("a good run should clear the error: %+v", st)
	}
}

func TestRefresher_EveryRunsOnATimerAndStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	r := NewRefresher(ctx, func(ctx context.Context) (Summary, error) {
		calls.Add(1)
		return Summary{}, nil
	})

	r.Every(10 * time.Millisecond)
	deadline := time.After(5 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d refreshes ran", calls.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if r.Status().Every != "10ms" {
		t.Errorf("Every = %q", r.Status().Every)
	}

	cancel()
	r.Wait() // returns only if the timer goroutine and any refresh have stopped
	after := calls.Load()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != after {
		t.Error("refreshes kept starting after cancel")
	}
	if r.Start() {
		t.Error("Start after cancel should do nothing")
	}
}

func TestRefresher_CancelReachesARunningRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	r := NewRefresher(ctx, func(ctx context.Context) (Summary, error) {
		close(started)
		<-ctx.Done()
		return Summary{}, ctx.Err()
	})
	r.Start()
	<-started

	cancel()
	r.Wait()

	if st := r.Status(); st.Running || st.LastError == "" {
		t.Errorf("a cancelled refresh should end with its error: %+v", st)
	}
}
