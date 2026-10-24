package workflows_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

// a is used only to name activities in mocks; it is never called.
var a *workflows.Activities

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	s.SetLogger(log.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	env := s.NewTestWorkflowEnvironment()
	workflows.Register(env, &workflows.Activities{})
	t.Cleanup(func() { env.AssertExpectations(t) })
	return env
}

func items(n int, players ...int) []news.Item {
	out := make([]news.Item, n)
	for i := range out {
		out[i] = news.Item{Source: "bbc", Title: fmt.Sprintf("Story %d", i), URL: fmt.Sprintf("https://x.test/%d", i), PlayerIDs: players}
	}
	return out
}

func TestNewsRefresh_AFailingFeedIsRecordedAndTheRestAreEmbeddedInChunks(t *testing.T) {
	env := newEnv(t)
	note := news.Item{Source: news.SourceFPL, Title: "Saka: knock", URL: "fpl://player/7/x", PlayerIDs: []int{7}}
	env.OnActivity(a.RefreshPlayers, mock.Anything).Return(workflows.PlayersRefreshed{
		FPLItems: []news.Item{note},
		Changes:  []workflows.StatusChange{{PlayerID: 7, Name: "Saka", Status: "doubtful (75%)", News: "Knock"}},
	}, nil)
	env.OnActivity(a.FetchFeed, mock.Anything, "bbc").Return(items(3), nil)
	env.OnActivity(a.FetchFeed, mock.Anything, "sky").Return(nil, errors.New("503"))
	todo := append(items(69), news.Item{Source: "sky", Title: "Saka limps off", URL: "https://sky.test/saka", PlayerIDs: []int{7}}, note)
	env.OnActivity(a.PrepareNews, mock.Anything, mock.Anything).Return(workflows.Prepared{Todo: todo, Fetched: 80, Duplicates: 9, Watched: []int{7}}, nil)
	env.OnActivity(a.EmbedAndSave, mock.Anything, mock.Anything).Return(nil).Times(2) // 71 items: 64 + 7

	var signals []string
	env.OnSignalExternalWorkflow(mock.Anything, "watch-7", "", mock.Anything, mock.Anything).Return(
		func(_, _, _, name string, arg any) error {
			signals = append(signals, name)
			return nil
		})

	env.ExecuteWorkflow(workflows.NewsRefresh, workflows.NewsInput{Feeds: []string{"bbc", "sky"}})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var res workflows.NewsResult
	_ = env.GetWorkflowResult(&res)
	if res.New != 71 || res.Fetched != 80 || res.Duplicates != 9 || len(res.FailedFeeds) != 1 || res.FailedFeeds[0] != "sky" || res.Linked != 2 {
		t.Errorf("result = %+v", res)
	}
	// One status change and one story about him; his FPL note is the status change, not a second story.
	if strings.Join(signals, ",") != "status,news" {
		t.Errorf("signals = %v", signals)
	}
}

func TestWatchPlayer_AlertsOnNewsAndOnRealStatusChangesOnly(t *testing.T) {
	env := newEnv(t)
	var got []notify.Message
	env.OnActivity(a.Notify, mock.Anything, mock.Anything).Return(func(_ context.Context, m notify.Message) error {
		got = append(got, m)
		return nil
	})
	status := workflows.StatusSignal{Name: "Palmer", Status: "suspended", News: "Suspended for 1 match"}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(workflows.SignalStatus, status) }, time.Minute)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(workflows.SignalStatus, status) }, 2*time.Minute) // unchanged
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflows.SignalNews, workflows.NewsSignal{Title: "Palmer sent off", URL: "https://bbc.test/p", Source: "bbc"})
	}, 3*time.Minute)
	env.RegisterDelayedCallback(env.CancelWorkflow, 4*time.Minute)

	env.ExecuteWorkflow(workflows.WatchPlayer, workflows.WatchInput{PlayerID: 4, Name: "Palmer"})

	if !env.IsWorkflowCompleted() {
		t.Fatal("unwatching should end the watch")
	}
	if len(got) != 2 {
		t.Fatalf("alerts = %+v, want the status change and the story", got)
	}
	if got[0].Title != "Palmer: suspended" || got[0].Body != "Suspended for 1 match" || got[0].PlayerID != 4 {
		t.Errorf("status alert = %+v", got[0])
	}
	if got[1].Title != "Palmer in the news" || got[1].URL != "https://bbc.test/p" || !strings.Contains(got[1].Body, "(bbc)") {
		t.Errorf("news alert = %+v", got[1])
	}
}

func TestWatchPlayer_ContinuesAsNewAfterManyEventsKeepingItsLastStatus(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.Notify, mock.Anything, mock.Anything).Return(nil)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflows.SignalStatus, workflows.StatusSignal{Status: "injured", News: "Knee"})
		for range 199 {
			env.SignalWorkflow(workflows.SignalNews, workflows.NewsSignal{Title: "x"})
		}
	}, time.Minute)

	env.ExecuteWorkflow(workflows.WatchPlayer, workflows.WatchInput{PlayerID: 4, Name: "Palmer"})

	var can *workflow.ContinueAsNewError
	if err := env.GetWorkflowError(); !errors.As(err, &can) {
		t.Fatalf("err = %v, want continue-as-new", err)
	}
}

func TestGameweekBriefing_WaitsUntilADayBeforeTheDeadline(t *testing.T) {
	env := newEnv(t)
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	env.SetStartTime(start)
	deadline := start.Add(4 * 24 * time.Hour)
	env.OnWorkflow(workflows.NewsRefresh, mock.Anything, mock.Anything).Return(workflows.NewsResult{}, nil)
	var sentAt time.Time
	env.OnActivity(a.SendBriefing, mock.Anything, 9, mock.Anything).Return(func(_ context.Context, _ int, _ time.Time) error {
		sentAt = env.Now()
		return nil
	})

	env.ExecuteWorkflow(workflows.GameweekBriefing, workflows.BriefingInput{Gameweek: 9, Deadline: deadline})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if want := deadline.Add(-24 * time.Hour); sentAt.Before(want) || sentAt.After(want.Add(time.Minute)) {
		t.Errorf("sent at %v, want about %v", sentAt, want)
	}
}

func TestGameweekBriefing_SendsAtOnceInsideTheLastDayAndNeverAfterTheDeadline(t *testing.T) {
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

	env := newEnv(t)
	env.SetStartTime(start)
	env.OnWorkflow(workflows.NewsRefresh, mock.Anything, mock.Anything).Return(workflows.NewsResult{}, errors.New("feeds down"))
	env.OnActivity(a.SendBriefing, mock.Anything, 9, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(workflows.GameweekBriefing, workflows.BriefingInput{Gameweek: 9, Deadline: start.Add(3 * time.Hour)})
	if err := env.GetWorkflowError(); err != nil {
		t.Errorf("a failed news pull should not stop the briefing: %v", err)
	}

	late := newEnv(t) // SendBriefing is not mocked: calling it would fail the test
	late.SetStartTime(start)
	late.ExecuteWorkflow(workflows.GameweekBriefing, workflows.BriefingInput{Gameweek: 9, Deadline: start.Add(-time.Hour)})
	if err := late.GetWorkflowError(); err != nil {
		t.Error(err)
	}
}

func TestBriefingCheck_StartsTheNextGameweeksBriefingOnlyBeforeItsDeadline(t *testing.T) {
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		next      workflows.Deadline
		wantChild bool
	}{
		{"upcoming", workflows.Deadline{Gameweek: 9, At: start.Add(48 * time.Hour)}, true},
		{"passed", workflows.Deadline{Gameweek: 9, At: start.Add(-time.Hour)}, false},
		{"season over", workflows.Deadline{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			env.SetStartTime(start)
			env.OnActivity(a.NextDeadline, mock.Anything).Return(tt.next, nil)
			started := false
			env.OnWorkflow(workflows.GameweekBriefing, mock.Anything, mock.Anything).Return(func(_ workflow.Context, in workflows.BriefingInput) error {
				started = in.Gameweek == 9
				return nil
			}).Maybe()

			env.ExecuteWorkflow(workflows.BriefingCheck, workflows.BriefingCheckInput{})

			if err := env.GetWorkflowError(); err != nil || started != tt.wantChild {
				t.Errorf("err=%v started=%v, want %v", err, started, tt.wantChild)
			}
		})
	}
}

func TestFullSync(t *testing.T) {
	ids := make([]int, 40)
	for i := range ids {
		ids[i] = i + 1
	}
	tests := []struct {
		name     string
		failing  int // players whose history cannot be fetched
		needed   bool
		wantErr  bool
		wantDone int
	}{
		{"up to date: nothing to do", 0, false, false, 0},
		{"a few failures are recorded", 3, true, false, 37},
		{"many failures stop it", 25, true, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			env.OnActivity(a.CheckSync, mock.Anything, false).Return(workflows.SyncPlan{Needed: tt.needed, PlayerIDs: ids, LastFinished: 8}, nil)
			env.OnActivity(a.SyncPlayerHistory, mock.Anything, mock.Anything).Return(func(_ context.Context, id int) (int, error) {
				if id <= tt.failing {
					return 0, errors.New("timeout")
				}
				return 8, nil
			}).Maybe()
			backtest := false
			env.OnWorkflow(workflows.Backtest, mock.Anything).Return(func(workflow.Context) (workflows.BacktestOutcome, error) {
				backtest = true
				return workflows.BacktestOutcome{}, nil
			}).Maybe()

			env.ExecuteWorkflow(workflows.FullSync, workflows.SyncInput{})

			err := env.GetWorkflowError()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr {
				if backtest {
					t.Error("a failed sync should not backtest")
				}
				return
			}
			var res workflows.SyncResult
			_ = env.GetWorkflowResult(&res)
			if res.Players != tt.wantDone || len(res.Failed) != tt.failing || backtest != tt.needed {
				t.Errorf("result %+v backtest=%v", res, backtest)
			}
			if tt.needed && res.Rows != 8*tt.wantDone {
				t.Errorf("rows = %d", res.Rows)
			}
		})
	}
}

func TestBacktestAndNightlyEval_AlertOnlyWhenThingsGetWorse(t *testing.T) {
	tests := []struct {
		name      string
		run       func(env *testsuite.TestWorkflowEnvironment)
		wantAlert string
	}{
		{"backtest steady", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunBacktest, mock.Anything).Return(workflows.BacktestOutcome{Spearman: 0.40, Previous: 0.42, HadPrior: true}, nil)
			env.ExecuteWorkflow(workflows.Backtest)
		}, ""},
		{"backtest first run", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunBacktest, mock.Anything).Return(workflows.BacktestOutcome{Spearman: 0.1}, nil)
			env.ExecuteWorkflow(workflows.Backtest)
		}, ""},
		{"backtest dropped", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunBacktest, mock.Anything).Return(workflows.BacktestOutcome{Through: 8, Spearman: 0.30, Previous: 0.42, HadPrior: true}, nil)
			env.ExecuteWorkflow(workflows.Backtest)
		}, "The scoring model got worse"},
		{"eval passing", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunEval, mock.Anything).Return(workflows.EvalOutcome{PassRate: 0.96}, nil)
			env.ExecuteWorkflow(workflows.NightlyEval)
		}, ""},
		{"eval skipped without a key", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunEval, mock.Anything).Return(workflows.EvalOutcome{Skipped: true}, nil)
			env.ExecuteWorkflow(workflows.NightlyEval)
		}, ""},
		{"eval failing", func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(a.RunEval, mock.Anything).Return(workflows.EvalOutcome{PassRate: 0.8, Model: "haiku", Failing: []string{"captain", "wildcard"}}, nil)
			env.ExecuteWorkflow(workflows.NightlyEval)
		}, "Assistant eval at 80%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			var got []notify.Message
			env.OnActivity(a.Notify, mock.Anything, mock.Anything).Return(func(_ context.Context, m notify.Message) error {
				got = append(got, m)
				return nil
			}).Maybe()

			tt.run(env)

			if err := env.GetWorkflowError(); err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.wantAlert == "" && len(got) != 0:
				t.Errorf("unexpected alert %+v", got)
			case tt.wantAlert != "" && (len(got) != 1 || got[0].Title != tt.wantAlert || got[0].Kind != "health"):
				t.Errorf("alerts = %+v, want %q", got, tt.wantAlert)
			}
		})
	}
}

func TestRelink_PagesUntilDoneCarryingTheCursor(t *testing.T) {
	env := newEnv(t)
	var cursors []int64
	env.OnActivity(a.RelinkPage, mock.Anything, mock.Anything).Return(func(_ context.Context, after int64) (workflows.PageResult, error) {
		cursors = append(cursors, after)
		return workflows.PageResult{Next: after + 200, Changed: 3, Done: after >= 400}, nil
	})

	env.ExecuteWorkflow(workflows.Relink, workflows.BackfillInput{})

	var res workflows.BackfillResult
	if err := env.GetWorkflowResult(&res); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cursors) != "[0 200 400]" || res.Pages != 3 || res.Changed != 9 {
		t.Errorf("cursors %v result %+v", cursors, res)
	}
}

func TestReembed_ContinuesAsNewOnALongBackfill(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.ReembedPage, mock.Anything, mock.Anything).Return(func(_ context.Context, after int64) (workflows.PageResult, error) {
		return workflows.PageResult{Next: after + 64, Changed: 64}, nil
	})

	env.ExecuteWorkflow(workflows.Reembed, workflows.BackfillInput{})

	var can *workflow.ContinueAsNewError
	if err := env.GetWorkflowError(); !errors.As(err, &can) {
		t.Fatalf("err = %v, want continue-as-new after 100 pages", err)
	}
}
