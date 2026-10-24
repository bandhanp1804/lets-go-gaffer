package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/eval"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

type evalOpts struct {
	filter  string
	runs    int
	workers int
	min     float64
}

func parseEvalArgs(args []string) (evalOpts, error) {
	o := evalOpts{}
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.filter, "run", "", "only cases whose name contains this text")
	fs.IntVar(&o.runs, "n", 3, "runs per case")
	fs.IntVar(&o.workers, "workers", 2, "runs in flight at once")
	fs.Float64Var(&o.min, "min", 0.9, "pass rate required for a zero exit")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w\nusage: gaffer eval [-run NAME] [-n 3] [-workers 2] [-min 0.9]", err)
	}
	switch {
	case fs.NArg() != 0:
		return o, errors.New("eval takes flags only; usage: gaffer eval [-run NAME] [-n 3] [-workers 2] [-min 0.9]")
	case o.runs < 1 || o.workers < 1:
		return o, errors.New("-n and -workers must be at least 1")
	case o.min < 0 || o.min > 1:
		return o, errors.New("-min is a fraction between 0 and 1")
	}
	return o, nil
}

// selectCases keeps the cases whose name contains filter.
func selectCases(all []eval.Case, filter string) []eval.Case {
	var out []eval.Case
	for _, c := range all {
		if strings.Contains(c.Name, filter) {
			out = append(out, c)
		}
	}
	return out
}

// runEval runs the assistant against the stub backend (fixed data, so a run measures
// the model and prompt, not this week's FPL) with the real model, and fails when the
// pass rate is below -min. It needs ANTHROPIC_API_KEY and spends tokens.
func runEval(ctx context.Context, w io.Writer, args []string) error {
	o, err := parseEvalArgs(args)
	if err != nil {
		return err
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return errors.New("ANTHROPIC_API_KEY is not set (add it to .env)")
	}
	cases := selectCases(eval.Cases(), o.filter)
	if len(cases) == 0 {
		return fmt.Errorf("no case name contains %q", o.filter)
	}
	model, err := newAnthropic(key)
	if err != nil {
		return err
	}
	fix := eval.NewFixture()
	a := assistant.New(model, fix, assistant.Config{Model: llm.ModelFromEnv(), Clubs: fix.Clubs()})

	fmt.Fprintf(w, "model %s, %d cases x %d runs\n\n", llm.ModelFromEnv(), len(cases), o.runs)
	results := eval.Runner{NewSession: a.NewSession, Runs: o.runs, Workers: o.workers}.Run(ctx, cases)
	eval.Write(w, results)

	if rate := eval.PassRate(results); rate < o.min {
		return fmt.Errorf("pass rate %.1f%% is below the required %.1f%%", rate*100, o.min*100)
	}
	return nil
}
