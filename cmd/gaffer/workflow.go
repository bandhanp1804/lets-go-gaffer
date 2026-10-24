package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

const workflowUsage = "usage: gaffer workflow [-detach] news | sync [-force] | backtest | eval | relink | reembed | briefing"

// manualWorkflow is one workflow `gaffer workflow` can start by hand.
type manualWorkflow struct {
	fn     any
	args   func(force bool) []any
	result func() any // a value to decode the result into, printed when the run finishes
}

func manualWorkflows() map[string]manualWorkflow {
	none := func(bool) []any { return nil }
	return map[string]manualWorkflow{
		"news":     {workflows.NewsRefresh, func(bool) []any { return []any{workflows.NewsInput{Feeds: feedNames()}} }, func() any { return &workflows.NewsResult{} }},
		"sync":     {workflows.FullSync, func(f bool) []any { return []any{workflows.SyncInput{Force: f}} }, func() any { return &workflows.SyncResult{} }},
		"backtest": {workflows.Backtest, none, func() any { return &workflows.BacktestOutcome{} }},
		"eval":     {workflows.NightlyEval, none, func() any { return &workflows.EvalOutcome{} }},
		"relink":   {workflows.Relink, func(bool) []any { return []any{workflows.BackfillInput{}} }, func() any { return &workflows.BackfillResult{} }},
		"reembed":  {workflows.Reembed, func(bool) []any { return []any{workflows.BackfillInput{}} }, func() any { return &workflows.BackfillResult{} }},
		"briefing": {workflows.BriefingCheck, func(bool) []any { return []any{workflows.BriefingCheckInput{Feeds: feedNames()}} }, func() any { return nil }},
	}
}

type workflowOpts struct {
	name   string
	force  bool
	detach bool
}

func parseWorkflowArgs(args []string) (workflowOpts, error) {
	var o workflowOpts
	fs := flag.NewFlagSet("workflow", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.detach, "detach", false, "start it and return without waiting")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w\n%s", err, workflowUsage)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return o, errors.New(workflowUsage)
	}
	o.name, rest = rest[0], rest[1:]
	if _, ok := manualWorkflows()[o.name]; !ok {
		names := make([]string, 0)
		for n := range manualWorkflows() {
			names = append(names, n)
		}
		sort.Strings(names)
		return o, fmt.Errorf("no workflow called %q (try %s)\n%s", o.name, strings.Join(names, ", "), workflowUsage)
	}
	for _, a := range rest {
		switch {
		case a == "-force" && o.name == "sync":
			o.force = true
		default:
			return o, fmt.Errorf("unexpected %q\n%s", a, workflowUsage)
		}
	}
	return o, nil
}

// runWorkflow starts one workflow by hand and, unless -detach, waits and prints its result.
// The worker (`gaffer worker`) must be running to pick it up.
func runWorkflow(ctx context.Context, w io.Writer, args []string) error {
	o, err := parseWorkflowArgs(args)
	if err != nil {
		return err
	}
	c, err := dialTemporal(ctx, temporalAddress(true))
	if err != nil {
		return err
	}
	defer c.Close()

	mw := manualWorkflows()[o.name]
	id := fmt.Sprintf("manual-%s-%d", o.name, time.Now().Unix())
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: workflows.TaskQueue}, mw.fn, mw.args(o.force)...)
	if err != nil {
		return fmt.Errorf("start %s: %w", o.name, err)
	}
	fmt.Fprintf(w, "started %s (run %s)\n%s\n", id, run.GetRunID(), workflowLink(id))
	if o.detach {
		return nil
	}
	fmt.Fprintln(w, "waiting for it to finish (Ctrl-C leaves it running; is `gaffer worker` running?)")
	res := mw.result()
	if err := run.Get(ctx, res); err != nil {
		return fmt.Errorf("%s: %w", o.name, err)
	}
	if res != nil {
		fmt.Fprintf(w, "done: %+v\n", res)
	} else {
		fmt.Fprintln(w, "done")
	}
	return nil
}
