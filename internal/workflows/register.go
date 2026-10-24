package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

// Register puts every workflow and the activities on a worker.
func Register(w worker.Registry, a *Activities) {
	for _, wf := range []any{NewsRefresh, WatchPlayer, BriefingCheck, GameweekBriefing, FullSync, Backtest, NightlyEval, Relink, Reembed} {
		w.RegisterWorkflow(wf)
	}
	w.RegisterActivity(a)
}

// ScheduleConfig is how often the scheduled workflows run. A zero NewsEvery or an empty
// EvalCron turns that schedule off (and deletes it if it exists).
type ScheduleConfig struct {
	Feeds     []string
	NewsEvery time.Duration
	EvalCron  string // e.g. "0 3 * * *"
	TimeZone  string // IANA name for EvalCron, e.g. "Europe/London"; empty is UTC
}

// Schedule ids.
const (
	ScheduleNews     = "gaffer-news"
	ScheduleBriefing = "gaffer-briefing-check"
	ScheduleSync     = "gaffer-full-sync"
	ScheduleEval     = "gaffer-nightly-eval"
)

type schedule struct {
	id     string
	spec   client.ScheduleSpec
	action *client.ScheduleWorkflowAction
	off    bool
}

func schedules(cfg ScheduleConfig) []schedule {
	every := func(d time.Duration) client.ScheduleSpec {
		return client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: d}}}
	}
	act := func(id string, wf any, args ...any) *client.ScheduleWorkflowAction {
		return &client.ScheduleWorkflowAction{ID: id, Workflow: wf, Args: args, TaskQueue: TaskQueue}
	}
	return []schedule{
		{id: ScheduleNews, spec: every(cfg.NewsEvery), action: act("news-refresh", NewsRefresh, NewsInput{Feeds: cfg.Feeds}), off: cfg.NewsEvery <= 0},
		{id: ScheduleBriefing, spec: every(time.Hour), action: act("briefing-check", BriefingCheck, BriefingCheckInput{Feeds: cfg.Feeds})},
		{id: ScheduleSync, spec: every(6 * time.Hour), action: act("full-sync", FullSync, SyncInput{})},
		{id: ScheduleEval, spec: client.ScheduleSpec{CronExpressions: []string{cfg.EvalCron}, TimeZoneName: cfg.TimeZone},
			action: act("nightly-eval", NightlyEval), off: cfg.EvalCron == ""},
	}
}

// EnsureSchedules creates the schedules, or brings existing ones up to date, so restarting the
// worker with new settings never leaves two of the same schedule. Each run of a schedule skips
// if the previous one is still going.
func EnsureSchedules(ctx context.Context, c client.Client) func(cfg ScheduleConfig) error {
	return func(cfg ScheduleConfig) error {
		sc := c.ScheduleClient()
		for _, s := range schedules(cfg) {
			if s.off {
				err := sc.GetHandle(ctx, s.id).Delete(ctx)
				if err != nil && !isNotFound(err) {
					return fmt.Errorf("remove schedule %s: %w", s.id, err)
				}
				continue
			}
			_, err := sc.Create(ctx, client.ScheduleOptions{
				ID: s.id, Spec: s.spec, Action: s.action, Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			})
			if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
				err = sc.GetHandle(ctx, s.id).Update(ctx, client.ScheduleUpdateOptions{
					DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
						sch := in.Description.Schedule
						spec := s.spec
						sch.Spec, sch.Action = &spec, s.action
						if sch.Policy == nil {
							sch.Policy = &client.SchedulePolicies{}
						}
						sch.Policy.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
						return &client.ScheduleUpdate{Schedule: &sch}, nil
					},
				})
			}
			if err != nil {
				return fmt.Errorf("schedule %s: %w", s.id, err)
			}
		}
		return nil
	}
}
