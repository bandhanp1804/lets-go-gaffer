package news

import (
	"context"
	"sync"
	"time"
)

// RefreshStatus is what a Refresher reports: whether a refresh is running, how the last one went,
// and how often they run on their own.
type RefreshStatus struct {
	// Available is false when the server cannot refresh the news (no embedding key).
	Available bool `json:"available"`
	Running   bool `json:"running"`
	// Every is how often a refresh starts by itself ("30m"); empty when only a manual one runs.
	Every string `json:"every,omitempty"`
	// LastFinished is when the last refresh ended, as RFC 3339; empty before the first.
	LastFinished string `json:"last_finished,omitempty"`
	LastFetched  int    `json:"last_fetched"`
	LastNew      int    `json:"last_new"`
	// LastFailedFeeds names the feeds the last refresh could not read; the rest were still pulled.
	LastFailedFeeds []string `json:"last_failed_feeds"`
	// LastError is why the last refresh failed; empty if it worked.
	LastError string `json:"last_error,omitempty"`
}

// Refresher pulls the news again whenever asked and, if told to, on a timer. Only one refresh runs
// at a time, so a click during a scheduled run (or two clicks) starts nothing extra: the embedding
// service is rate limited and a second run would only wait behind the first.
type Refresher struct {
	ctx context.Context
	run func(ctx context.Context) (Summary, error)
	now func() time.Time

	wg sync.WaitGroup
	mu sync.Mutex
	st RefreshStatus
}

// NewRefresher returns a Refresher that calls run for each refresh. Refreshes stop when ctx is
// cancelled; call Wait to be sure they have.
func NewRefresher(ctx context.Context, run func(ctx context.Context) (Summary, error)) *Refresher {
	return &Refresher{ctx: ctx, run: run, now: time.Now}
}

// Start begins a refresh in the background. It reports false, and does nothing, if one is already running.
func (r *Refresher) Start() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.st.Running || r.ctx.Err() != nil {
		return false
	}
	r.st.Running = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		sum, err := r.run(r.ctx)

		r.mu.Lock()
		defer r.mu.Unlock()
		r.st.Running = false
		r.st.LastFinished = r.now().UTC().Format(time.RFC3339)
		r.st.LastFetched, r.st.LastNew, r.st.LastError = sum.Fetched, sum.New, ""
		r.st.LastFailedFeeds = sum.FailedSources()
		if err != nil {
			r.st.LastError = err.Error()
		}
	}()
	return true
}

// Status is a snapshot of the refresher.
func (r *Refresher) Status() RefreshStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.st
	st.Available = true
	return st
}

// Every starts a refresh now and then once per interval, until the Refresher's context is cancelled.
func (r *Refresher) Every(interval time.Duration) {
	r.mu.Lock()
	r.st.Every = interval.String()
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		r.Start()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-t.C:
				r.Start()
			}
		}
	}()
}

// Wait returns once every refresh and the timer have stopped, which they do when the context is cancelled.
func (r *Refresher) Wait() { r.wg.Wait() }
