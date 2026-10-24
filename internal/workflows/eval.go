package workflows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
)

// evalFloor is the pass rate below which a nightly eval raises an alert, the same bar as `gaffer eval`.
const evalFloor = 0.9

// EvalOutcome is one run of the eval cases.
type EvalOutcome struct {
	Skipped  bool // no ANTHROPIC_API_KEY
	Model    string
	PassRate float64
	Runs     int
	Failing  []string
	Tokens   int
}

// NightlyEval runs the assistant's eval cases once each, keeps the result, and raises an alert
// when the pass rate falls under 90%: a model update or a prompt change that quietly breaks
// answers shows up the next morning instead of when someone notices.
func NightlyEval(ctx workflow.Context) (EvalOutcome, error) {
	var a *Activities
	run := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	var out EvalOutcome
	if err := workflow.ExecuteActivity(run, a.RunEval).Get(ctx, &out); err != nil {
		return out, err
	}
	if out.Skipped || out.PassRate >= evalFloor {
		return out, nil
	}
	notifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})
	m := notify.Message{Kind: "health", Title: fmt.Sprintf("Assistant eval at %.0f%%", out.PassRate*100),
		Body: fmt.Sprintf("Below the %.0f%% bar on %s. Failing: %s. Run `gaffer eval` to see why.", evalFloor*100, out.Model, strings.Join(out.Failing, ", "))}
	return out, workflow.ExecuteActivity(notifyCtx, a.Notify, m).Get(ctx, nil)
}

// RunEval runs the cases and saves the result. It heartbeats while the model answers, so a
// worker that dies mid-run is noticed in minutes, not after the half-hour timeout.
func (a *Activities) RunEval(ctx context.Context) (EvalOutcome, error) {
	if a.Eval == nil {
		return EvalOutcome{Skipped: true}, nil
	}
	stop := make(chan struct{})
	beats := make(chan struct{})
	go func() { // stops when RunEval returns
		defer close(beats)
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	out, err := a.Eval.Evaluate(ctx)
	close(stop)
	<-beats
	if err != nil {
		return out, err
	}
	return out, a.Store.SaveEvalRun(ctx, db.EvalRun{Model: out.Model, PassRate: out.PassRate, Runs: out.Runs, Failing: out.Failing, Tokens: out.Tokens})
}
