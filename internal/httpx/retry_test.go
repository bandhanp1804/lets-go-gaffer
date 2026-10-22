package httpx_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

var errBoom = errors.New("boom")

// step is one scripted outcome of the underlying transport.
type step struct {
	status     int
	err        error
	retryAfter string
}

// trackedBody records whether the transport closed it, so we can assert that
// retried responses are released (needed for connection reuse).
type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

// scripted is a fake http.RoundTripper that replays steps in order.
type scripted struct {
	steps     []step
	calls     int
	bodies    []*trackedBody
	reqBodies []string // what each attempt's request body contained
}

func (s *scripted) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.reqBodies = append(s.reqBodies, string(b))
	}
	if s.calls >= len(s.steps) {
		return nil, fmt.Errorf("script exhausted: call %d", s.calls+1)
	}
	st := s.steps[s.calls]
	s.calls++
	if st.err != nil {
		return nil, st.err
	}
	body := &trackedBody{Reader: strings.NewReader("")}
	s.bodies = append(s.bodies, body)
	h := http.Header{}
	if st.retryAfter != "" {
		h.Set("Retry-After", st.retryAfter)
	}
	return &http.Response{StatusCode: st.status, Header: h, Body: body, Request: req}, nil
}

func repeat(n int, s step) []step {
	out := make([]step, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// sleeper replaces real sleeping and records the requested delays.
type sleeper struct{ delays []time.Duration }

func (s *sleeper) sleep(ctx context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return ctx.Err()
}

func get(ctx context.Context, rt http.RoundTripper) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://fpl.test/x", nil)
	if err != nil {
		return nil, err
	}
	return (&http.Client{Transport: rt}).Do(req)
}

func TestRetryTransport(t *testing.T) {
	const (
		maxAttempts = 4
		base        = 100 * time.Millisecond
		maxDelay    = 1 * time.Second
	)

	tests := []struct {
		name       string
		steps      []step
		wantCalls  int
		wantStatus int  // checked when the call should succeed
		giveUp     bool // want a *httpx.RetryError after exhausting attempts
		// With giveUp: the cause must be a *httpx.StatusError with this code,
		// or (when 0) wrap errBoom.
		causeStatus int
	}{
		{"success first try", []step{{status: 200}}, 1, 200, false, 0},
		{"recovers after two 503s", []step{{status: 503}, {status: 503}, {status: 200}}, 3, 200, false, 0},
		{"recovers after 429", []step{{status: 429}, {status: 200}}, 2, 200, false, 0},
		{"recovers after network error", []step{{err: errBoom}, {status: 200}}, 2, 200, false, 0},
		{"persistent 500 gives up", repeat(maxAttempts, step{status: 500}), maxAttempts, 0, true, 500},
		{"persistent network error gives up", repeat(maxAttempts, step{err: errBoom}), maxAttempts, 0, true, 0},
		// Non-retryable statuses are passed through untouched for the caller to judge.
		{"404 is not retried", []step{{status: 404}}, 1, 404, false, 0},
		{"400 is not retried", []step{{status: 400}}, 1, 400, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &scripted{steps: tc.steps}
			sl := &sleeper{}
			tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{
				MaxAttempts: maxAttempts,
				BaseDelay:   base,
				MaxDelay:    maxDelay,
				Sleep:       sl.sleep,
			})

			resp, err := get(context.Background(), tr)
			if resp != nil {
				defer resp.Body.Close()
			}

			if rt.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", rt.calls, tc.wantCalls)
			}
			if tc.giveUp {
				var re *httpx.RetryError
				if !errors.As(err, &re) {
					t.Fatalf("err = %v, want *httpx.RetryError", err)
				}
				if re.Attempts != tc.wantCalls {
					t.Errorf("RetryError.Attempts = %d, want %d", re.Attempts, tc.wantCalls)
				}
				if tc.causeStatus != 0 {
					var se *httpx.StatusError
					if !errors.As(err, &se) || se.StatusCode != tc.causeStatus {
						t.Errorf("err = %v, want cause *httpx.StatusError{%d}", err, tc.causeStatus)
					}
				} else if !errors.Is(err, errBoom) {
					t.Errorf("err = %v, want it to wrap %v", err, errBoom)
				}
				if resp != nil {
					t.Errorf("resp = %v, want nil when giving up", resp.Status)
				}
				// Nobody will read the final response, so the transport must release it.
				for i, b := range rt.bodies {
					if !b.closed {
						t.Errorf("body %d not closed after giving up", i)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.StatusCode != tc.wantStatus {
					t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
				}
			}

			// One sleep between each pair of attempts, never after the last.
			if len(sl.delays) != tc.wantCalls-1 {
				t.Fatalf("sleeps = %d, want %d", len(sl.delays), tc.wantCalls-1)
			}
			// Full jitter: each delay is in [0, min(maxDelay, base*2^i)].
			for i, d := range sl.delays {
				limit := min(base<<i, maxDelay)
				if d < 0 || d > limit {
					t.Errorf("delay[%d] = %v, want within [0, %v]", i, d, limit)
				}
			}
			// Every response we retried past must have been closed.
			for i := 0; i < len(rt.bodies)-1; i++ {
				if !rt.bodies[i].closed {
					t.Errorf("body %d of a retried response was not closed", i)
				}
			}
		})
	}
}

func TestRetryTransport_ReplaysTheRequestBodyOnEveryAttempt(t *testing.T) {
	rt := &scripted{steps: []step{{status: 503}, {status: 503}, {status: 200}}}
	tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, Sleep: (&sleeper{}).sleep})

	// strings.Reader gives the request a GetBody, which is how a body is replayed.
	req, _ := http.NewRequest(http.MethodPost, "http://fpl.test/embed", strings.NewReader(`{"input":["a"]}`))
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(rt.reqBodies) != 3 {
		t.Fatalf("attempts saw %d bodies, want 3", len(rt.reqBodies))
	}
	for i, got := range rt.reqBodies {
		if got != `{"input":["a"]}` {
			t.Errorf("attempt %d body = %q, want the full original body", i+1, got)
		}
	}
}

func TestRetryTransport_RefusesToRetryABodyItCannotReplay(t *testing.T) {
	rt := &scripted{steps: []step{{status: 503}, {status: 200}}}
	tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, Sleep: (&sleeper{}).sleep})

	// An arbitrary reader has no GetBody: a retry would send an empty body.
	req, _ := http.NewRequest(http.MethodPost, "http://fpl.test/embed", io.NopCloser(strings.NewReader("payload")))
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	if err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("err = %v, want an error explaining the body cannot be replayed", err)
	}
	if rt.calls != 1 {
		t.Errorf("calls = %d, want 1: never send a second attempt with a lost body", rt.calls)
	}
}

func TestRetryTransport_HonoursRetryAfter(t *testing.T) {
	rt := &scripted{steps: []step{{status: 429, retryAfter: "2"}, {status: 200}}}
	sl := &sleeper{}
	tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond, // deliberately below Retry-After
		Sleep:       sl.sleep,
	})

	resp, err := get(context.Background(), tr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if len(sl.delays) != 1 {
		t.Fatalf("sleeps = %d, want 1", len(sl.delays))
	}
	// Server instructions win over our own backoff cap.
	if sl.delays[0] < 2*time.Second {
		t.Errorf("delay = %v, want >= 2s (Retry-After)", sl.delays[0])
	}
}

func TestRetryTransport_RetryAfterIsCapped(t *testing.T) {
	rt := &scripted{steps: []step{{status: 429, retryAfter: "86400"}, {status: 200}}} // 24h
	sl := &sleeper{}
	tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		Sleep:       sl.sleep,
	})

	resp, err := get(context.Background(), tr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if len(sl.delays) != 1 || sl.delays[0] != 30*time.Minute {
		t.Errorf("delays = %v, want [30m0s]", sl.delays)
	}
}

func TestRetryTransport_ContextCancelStopsBackoff(t *testing.T) {
	rt := &scripted{steps: repeat(100, step{status: 503})}
	// Default Sleep (nil) with an hour-long backoff: only ctx can end this quickly.
	tr := httpx.NewRetryTransport(rt, httpx.RetryConfig{
		MaxAttempts: 100,
		BaseDelay:   time.Hour,
		MaxDelay:    time.Hour,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	resp, err := get(ctx, tr)
	if resp != nil {
		resp.Body.Close()
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to honour cancellation, want well under 1s", elapsed)
	}
}
