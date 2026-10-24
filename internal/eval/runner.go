package eval

import (
	"context"
	"fmt"
	"sync"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

// Case is one scenario: the questions asked in order in a single conversation, and
// the checks the final result must pass.
type Case struct {
	Name      string
	Questions []string
	Checks    []Check
}

// Runner runs cases against an assistant.
type Runner struct {
	// NewSession returns a fresh conversation, so runs do not see each other's history.
	NewSession func() *assistant.Session
	Runs       int // times each case is run, since model output varies; default 1
	Workers    int // runs in flight at once; default 2, low enough to respect API rate limits
}

// RunResult is the outcome of one run of one case.
type RunResult struct {
	Err      error            // the conversation itself failed (API error, cancelled)
	Failures map[string]error // check name -> why it failed
	Tokens   llm.Usage
	Text     string // the final answer, so a failure can be read rather than guessed at
}

// Passed reports whether the conversation completed and every check passed.
func (r RunResult) Passed() bool { return r.Err == nil && len(r.Failures) == 0 }

// CaseResult collects the runs of one case.
type CaseResult struct {
	Case string
	Runs []RunResult
}

// Passes counts the runs that passed.
func (c CaseResult) Passes() int {
	n := 0
	for _, r := range c.Runs {
		if r.Passed() {
			n++
		}
	}
	return n
}

type job struct{ ci, ri int }

// Run executes every case Runs times and returns the results in case order. Workers
// stop when ctx is cancelled; runs that did not start are reported with ctx's error.
func (r Runner) Run(ctx context.Context, cases []Case) []CaseResult {
	runs, workers := max(r.Runs, 1), r.Workers
	if workers < 1 {
		workers = 2
	}
	out := make([]CaseResult, len(cases))
	for i, c := range cases {
		out[i] = CaseResult{Case: c.Name, Runs: make([]RunResult, runs)}
	}

	jobs := make(chan job)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs { // each job writes its own slot, so no lock is needed
				out[j.ci].Runs[j.ri] = r.runOnce(ctx, cases[j.ci])
			}
		}()
	}
	for ci := range cases {
		for ri := range runs {
			jobs <- job{ci, ri} // workers never block forever: runOnce returns promptly once ctx is done
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func (r Runner) runOnce(ctx context.Context, c Case) RunResult {
	if err := ctx.Err(); err != nil {
		return RunResult{Err: err}
	}
	s := r.NewSession()
	var res Result
	var rr RunResult
	for _, q := range c.Questions {
		a, err := s.Ask(ctx, q, nil)
		if err != nil {
			rr.Err = fmt.Errorf("asking %q: %w", q, err)
			return rr
		}
		res.Answers = append(res.Answers, a)
		rr.Tokens.InputTokens += a.Usage.InputTokens
		rr.Tokens.OutputTokens += a.Usage.OutputTokens
		rr.Tokens.CacheReadTokens += a.Usage.CacheReadTokens
		rr.Tokens.CacheCreationTokens += a.Usage.CacheCreationTokens
	}
	rr.Text = res.Last().Text
	for _, ch := range c.Checks {
		if err := ch.Run(res); err != nil {
			if rr.Failures == nil {
				rr.Failures = map[string]error{}
			}
			rr.Failures[ch.Name] = err
		}
	}
	return rr
}
