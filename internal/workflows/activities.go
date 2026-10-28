// Package workflows is the background work that Temporal runs for gaffer: pulling the news,
// watching players, the gameweek briefing, the full sync and backtest, the nightly eval, and
// backfills. Workflows decide what happens and in what order; activities, the methods of
// Activities, do the work that touches the network or the database. Workflow code calls only
// activities, timers and signals, and reads time with workflow.Now, so Temporal can replay it.
package workflows

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/temporal"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// TaskQueue is where gaffer's worker takes its work from.
const TaskQueue = "gaffer"

// NewsStore is what the news activities read and write. *db.Store satisfies it.
type NewsStore interface {
	ListPlayers(ctx context.Context) ([]fpl.Player, error)
	ListTeams(ctx context.Context) ([]fpl.Team, error)
	PlayerStates(ctx context.Context) (map[int]db.PlayerState, error)
	WatchedPlayers(ctx context.Context) ([]int, error)
	Watchers(ctx context.Context, playerID int) ([]string, error)
	UpsertTeams(ctx context.Context, teams []fpl.Team) error
	UpsertPlayers(ctx context.Context, players []fpl.Player) error
	news.Store
}

// SyncStore is what the sync and backtest activities need.
type SyncStore interface {
	UpsertHistory(ctx context.Context, rows []fpl.PlayerHistory) error
	ListHistory(ctx context.Context) ([]fpl.PlayerHistory, error)
	SaveBacktestRuns(ctx context.Context, runs []db.BacktestRun) error
	BacktestRuns(ctx context.Context, model string, limit int) ([]db.BacktestRun, error)
}

// RunStore is what the briefing, eval and backfill activities need.
type RunStore interface {
	MarkBriefingSent(ctx context.Context, gameweek int) (bool, error)
	ReleaseBriefing(ctx context.Context, gameweek int) error
	SaveEvalRun(ctx context.Context, r db.EvalRun) error
	RelinkNewsPage(ctx context.Context, after int64, limit int, link func(string) []int) (db.RelinkPage, error)
	ChunksAfter(ctx context.Context, after int64, limit int) ([]db.Chunk, error)
	UpdateEmbeddings(ctx context.Context, vecs map[int64][]float32) error
}

// Store is all of them; *db.Store satisfies it.
type Store interface {
	NewsStore
	SyncStore
	RunStore
}

// FPL is the part of the FPL client the activities call.
type FPL interface {
	FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error)
	FetchPlayerHistory(ctx context.Context, playerID int) ([]fpl.PlayerHistory, error)
	FetchFixtures(ctx context.Context) (fpl.Fixtures, error)
}

// Advisor is the part of the service the briefing and the sync check read. *service.Service satisfies it.
type Advisor interface {
	Status(ctx context.Context) (service.StatusView, error)
	LineupWith(ctx context.Context, req service.LineupRequest) (service.LineupView, error)
	Transfers(ctx context.Context, req service.TransfersRequest) (service.TransfersView, error)
	ChipsFor(ctx context.Context, req service.ChipsRequest) (service.ChipsView, error)
}

// Evaluator runs the assistant's eval cases once each.
type Evaluator interface {
	Evaluate(ctx context.Context) (EvalOutcome, error)
}

// Activities holds what the activities use. Embedder and Eval may be nil: the news activities
// then fail without retrying, and the eval reports itself skipped.
type Activities struct {
	Store    Store
	FPL      FPL
	Feeds    map[string]news.Source // RSS sources by name
	Embedder news.Embedder
	Notifier notify.Notifier
	Advisor  Advisor
	Eval     Evaluator
}

// Notify delivers one message: into the inbox, and to the phone when ntfy is set up.
func (a *Activities) Notify(ctx context.Context, m notify.Message) error {
	return a.Notifier.Notify(ctx, m)
}

// NotifyWatchers delivers a message about a player to everyone who watches him, each in their own
// inbox ("" is the owners', the only one pushed to a phone).
func (a *Activities) NotifyWatchers(ctx context.Context, m notify.Message) error {
	users, err := a.Store.Watchers(ctx, m.PlayerID)
	if err != nil {
		return err
	}
	var errs []error
	for _, u := range users {
		m.User = u
		if err := a.Notifier.Notify(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// permanent marks an error no retry can fix, such as a missing API key.
func permanent(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return temporal.NewNonRetryableApplicationError(msg, "config", errors.New(msg))
}
