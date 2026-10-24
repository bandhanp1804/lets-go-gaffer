package workflows

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

const (
	// syncParallel is how many players' histories are fetched at once, as `gaffer sync` does.
	syncParallel = 8
	// syncMaxFailures is how many players may fail before the sync gives up: more than that
	// means FPL or the database is down, not one bad player.
	syncMaxFailures = 20
	// spearmanDrop is how far the default model's rank correlation may fall between backtests
	// before it is worth an alert.
	spearmanDrop = 0.05
)

// SyncInput forces a sync even when the stored history is up to date.
type SyncInput struct {
	Force bool
}

// SyncPlan is what CheckSync found: whether to sync, whom, and through which gameweek.
type SyncPlan struct {
	Needed       bool
	PlayerIDs    []int
	LastFinished int
}

// SyncResult is what a full sync did.
type SyncResult struct {
	Ran     bool `json:"ran"`
	Players int  `json:"players"`
	Rows    int  `json:"rows"`
	Failed  []int
}

// FullSync refreshes every player's gameweek history once a gameweek has finished, one activity
// per player so each is retried on its own, then backtests the scoring model on the new history.
func FullSync(ctx workflow.Context, in SyncInput) (SyncResult, error) {
	var a *Activities
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var plan SyncPlan
	if err := workflow.ExecuteActivity(quick, a.CheckSync, in.Force).Get(ctx, &plan); err != nil {
		return SyncResult{}, err
	}
	if !plan.Needed {
		return SyncResult{}, nil
	}

	player := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 4},
	})
	res := SyncResult{Ran: true}
	sel := workflow.NewSelector(ctx)
	pending := 0
	collect := func(id int) func(workflow.Future) {
		return func(f workflow.Future) {
			pending--
			var rows int
			if err := f.Get(ctx, &rows); err != nil {
				res.Failed = append(res.Failed, id)
				return
			}
			res.Players++
			res.Rows += rows
		}
	}
	for _, id := range plan.PlayerIDs {
		for pending >= syncParallel {
			sel.Select(ctx)
		}
		if len(res.Failed) > syncMaxFailures {
			break
		}
		sel.AddFuture(workflow.ExecuteActivity(player, a.SyncPlayerHistory, id), collect(id))
		pending++
	}
	for pending > 0 {
		sel.Select(ctx)
	}
	if len(res.Failed) > syncMaxFailures {
		return res, fmt.Errorf("sync stopped: %d players failed (first %v), so FPL or the database is likely down", len(res.Failed), res.Failed[:min(5, len(res.Failed))])
	}

	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            fmt.Sprintf("backtest-through-gw%d-%s", plan.LastFinished, workflow.GetInfo(ctx).WorkflowExecution.RunID[:8]),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	})
	if err := workflow.ExecuteChildWorkflow(child, Backtest).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("backtest after sync failed", "err", err)
	}
	return res, nil
}

// CheckSync refreshes the season snapshot and says whether a gameweek has finished since the
// stored history was last brought up to date.
func (a *Activities) CheckSync(ctx context.Context, force bool) (SyncPlan, error) {
	st, err := a.Advisor.Status(ctx)
	if err != nil {
		return SyncPlan{}, err
	}
	if !force && st.LastFinished <= st.HistoryThrough {
		return SyncPlan{}, nil
	}
	boot, err := ingest.Bootstrap(ctx, a.FPL, a.Store)
	if err != nil {
		return SyncPlan{}, err
	}
	plan := SyncPlan{Needed: true, LastFinished: fpl.LastFinishedGameweek(boot.Gameweeks)}
	for _, p := range boot.Players {
		plan.PlayerIDs = append(plan.PlayerIDs, p.ID)
	}
	return plan, nil
}

// SyncPlayerHistory fetches and stores one player's gameweek history and returns how many rows it had.
func (a *Activities) SyncPlayerHistory(ctx context.Context, playerID int) (int, error) {
	rows, err := a.FPL.FetchPlayerHistory(ctx, playerID)
	if err != nil {
		return 0, err
	}
	if err := a.Store.UpsertHistory(ctx, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// BacktestOutcome is a backtest's headline: the default model now and before.
type BacktestOutcome struct {
	Through  int
	Model    string
	Spearman float64
	Previous float64 // 0 when there was no earlier run
	HadPrior bool
}

// Dropped says whether the default model got worse by enough to mention.
func (o BacktestOutcome) Dropped() bool { return o.HadPrior && o.Previous-o.Spearman > spearmanDrop }

// Backtest measures the scoring models on the gameweeks played so far, keeps the result, and
// raises an alert if the default model ranks players noticeably worse than last time.
func Backtest(ctx workflow.Context) (BacktestOutcome, error) {
	var a *Activities
	run := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	var out BacktestOutcome
	if err := workflow.ExecuteActivity(run, a.RunBacktest).Get(ctx, &out); err != nil {
		return out, err
	}
	if out.Dropped() {
		notifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})
		m := notify.Message{Kind: "health", Title: "The scoring model got worse",
			Body: fmt.Sprintf("Through GW%d its rank correlation is %.2f, down from %.2f. Check the Backtest page.", out.Through, out.Spearman, out.Previous)}
		if err := workflow.ExecuteActivity(notifyCtx, a.Notify, m).Get(ctx, nil); err != nil {
			return out, err
		}
	}
	return out, nil
}

// RunBacktest backtests every model on the stored history and saves one row per model.
func (a *Activities) RunBacktest(ctx context.Context) (BacktestOutcome, error) {
	players, err := a.Store.ListPlayers(ctx)
	if err != nil {
		return BacktestOutcome{}, err
	}
	history, err := a.Store.ListHistory(ctx)
	if err != nil {
		return BacktestOutcome{}, err
	}
	fixtures, err := a.FPL.FetchFixtures(ctx)
	if err != nil {
		return BacktestOutcome{}, err
	}
	rep, err := projection.RunBacktest(ctx, players, history, fixtures)
	if err != nil {
		return BacktestOutcome{}, permanent("backtest: %v", err)
	}
	return a.saveBacktest(ctx, rep)
}

func (a *Activities) saveBacktest(ctx context.Context, rep projection.BacktestReport) (BacktestOutcome, error) {
	out := BacktestOutcome{Through: rep.Last}
	rows := make([]db.BacktestRun, 0, len(rep.Models))
	for _, m := range rep.Models {
		rows = append(rows, db.BacktestRun{ThroughGW: rep.Last, Model: m.Name, Spearman: m.Spearman, MAE: m.MAE, Top20: m.TopMean})
		if m.Name == projection.DefaultModelName {
			out.Model, out.Spearman = m.Name, m.Spearman
		}
	}
	if out.Model != "" {
		prev, err := a.Store.BacktestRuns(ctx, out.Model, 1)
		if err != nil {
			return out, err
		}
		if len(prev) == 1 {
			out.Previous, out.HadPrior = prev[0].Spearman, true
		}
	}
	return out, a.Store.SaveBacktestRuns(ctx, rows)
}
