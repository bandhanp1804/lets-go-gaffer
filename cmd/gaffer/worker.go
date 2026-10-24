package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/worker"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

// defaultEvalCron is when the nightly eval runs, in the worker's local time zone.
const defaultEvalCron = "0 3 * * *"

// runWorker runs gaffer's workflows until Ctrl-C: it registers them with Temporal, makes sure the
// schedules exist, restarts the watches of watched players, and works the task queue.
func runWorker(ctx context.Context, w io.Writer) error {
	st, closeStack, err := buildStack(ctx)
	if err != nil {
		return err
	}
	defer closeStack()
	c, err := dialTemporal(ctx, temporalAddress(true))
	if err != nil {
		return err
	}
	defer c.Close()

	acts := &workflows.Activities{
		Store:    st.store,
		FPL:      fpl.NewHTTPClient(politeClient(100 * time.Millisecond)),
		Feeds:    feedSources(),
		Notifier: notifier(st.store),
		Advisor:  st.svc,
	}
	if st.embedder != nil { // a nil *Voyage in the interface would not read as nil
		acts.Embedder = st.embedder
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		model, err := newAnthropic(key)
		if err != nil {
			return err
		}
		acts.Eval = evaluator{model: model}
	}

	every, err := parseNewsRefresh(os.Getenv("GAFFER_NEWS_REFRESH"))
	if err != nil {
		return err
	}
	evalCron := defaultEvalCron
	if v, ok := os.LookupEnv("GAFFER_EVAL_SCHEDULE"); ok {
		if evalCron = strings.TrimSpace(v); evalCron == "off" {
			evalCron = ""
		}
	}
	if err := workflows.EnsureSchedules(ctx, c)(workflows.ScheduleConfig{
		Feeds: feedNames(), NewsEvery: every, EvalCron: evalCron, TimeZone: localZone(),
	}); err != nil {
		return err
	}
	if err := restartWatches(ctx, st, workflows.Watcher{Client: c}); err != nil {
		return err
	}

	wk := worker.New(c, workflows.TaskQueue, worker.Options{})
	workflows.Register(wk, acts)
	if err := wk.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	news := "off"
	if every > 0 {
		news = "every " + every.String()
	}
	eval := "off"
	if evalCron != "" {
		eval = fmt.Sprintf("%q (%s)", evalCron, localZone())
	}
	fmt.Fprintf(w, "gaffer worker on task queue %q. Schedules: news %s, briefing check hourly, full sync check every 6h, eval %s.\nWeb UI: %s\n",
		workflows.TaskQueue, news, eval, temporalUI())
	if acts.Eval == nil {
		fmt.Fprintln(w, "ANTHROPIC_API_KEY is not set: the nightly eval will report itself skipped.")
	}
	<-ctx.Done()
	wk.Stop() // waits for activities in flight to return
	return nil
}

// restartWatches makes sure every watched player has a running watch, for a watchlist kept while
// the worker was away or before Temporal was set up.
func restartWatches(ctx context.Context, st stack, w workflows.Watcher) error {
	ids, err := st.store.Watchlist(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	players, err := st.store.ListPlayers(ctx)
	if err != nil {
		return err
	}
	names := make(map[int]string, len(players))
	for _, p := range players {
		names[p.ID] = p.WebName
	}
	for _, id := range ids {
		if err := w.Watch(ctx, id, names[id]); err != nil {
			return err
		}
	}
	slog.Info("watches running", "players", len(ids))
	return nil
}

// localZone is the IANA name of the machine's zone for the eval's cron, or UTC.
func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			return target[i+len("zoneinfo/"):]
		}
	}
	return "UTC"
}
