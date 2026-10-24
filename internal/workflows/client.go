package workflows

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// Refresher starts and follows news pulls through Temporal, for the API's Refresh button. It
// reports on every NewsRefresh run, scheduled or clicked, so the page shows the latest either way.
type Refresher struct {
	Client client.Client
	Feeds  []string
	Every  time.Duration // the schedule's interval, shown on the page; 0 when it is off
}

const manualNewsID = "news-refresh-manual"

// Start begins a pull unless one is already running. It reports whether it started one.
func (r Refresher) Start(ctx context.Context) (bool, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return false, err
	}
	if st.Running {
		return false, nil
	}
	_, err = r.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: manualNewsID, TaskQueue: TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, NewsRefresh, NewsInput{Feeds: r.Feeds})
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &started) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("start news pull: %w", err)
	}
	return true, nil
}

// Status reads the newest NewsRefresh runs: whether one is going, and how the last finished one went.
func (r Refresher) Status(ctx context.Context) (news.RefreshStatus, error) {
	st := news.RefreshStatus{Available: true}
	if r.Every > 0 {
		st.Every = r.Every.String()
	}
	resp, err := r.Client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Query:    "WorkflowType = 'NewsRefresh'",
		PageSize: 20,
	})
	if err != nil {
		return st, fmt.Errorf("list news pulls: %w", err)
	}
	// Newest first. The dev server's store cannot ORDER BY, so the order is set here.
	execs := resp.Executions
	sort.SliceStable(execs, func(i, j int) bool { return execs[i].StartTime.AsTime().After(execs[j].StartTime.AsTime()) })
	for _, e := range execs {
		switch e.Status {
		case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
			st.Running = true
			continue
		case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
			enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		default:
			continue // cancelled or terminated runs say nothing about the news
		}
		if st.LastFinished != "" {
			continue
		}
		if e.CloseTime != nil {
			st.LastFinished = e.CloseTime.AsTime().UTC().Format(time.RFC3339)
		}
		var res NewsResult
		if err := r.Client.GetWorkflow(ctx, e.Execution.WorkflowId, e.Execution.RunId).Get(ctx, &res); err != nil {
			st.LastError = err.Error()
		} else {
			st.LastFetched, st.LastNew, st.LastFailedFeeds = res.Fetched, res.New, res.FailedFeeds
		}
	}
	return st, nil
}

// Watcher starts and stops the watch workflows.
type Watcher struct{ Client client.Client }

// Watch starts a player's watch, or leaves the one already running.
func (w Watcher) Watch(ctx context.Context, playerID int, name string) error {
	_, err := w.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: WatchID(playerID), TaskQueue: TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, WatchPlayer, WatchInput{PlayerID: playerID, Name: name})
	if err != nil {
		return fmt.Errorf("start watch of %s: %w", name, err)
	}
	return nil
}

// Unwatch stops a player's watch; one that is not running is not an error.
func (w Watcher) Unwatch(ctx context.Context, playerID int) error {
	err := w.Client.CancelWorkflow(ctx, WatchID(playerID), "")
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("stop watch of player %d: %w", playerID, err)
	}
	return nil
}

func isNotFound(err error) bool {
	var nf *serviceerror.NotFound
	return errors.As(err, &nf)
}
