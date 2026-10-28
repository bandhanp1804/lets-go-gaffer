package service

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// InboxStore is what the inbox reads and writes. *db.Store satisfies it.
type InboxStore interface {
	ListAlerts(ctx context.Context, user string, limit int) ([]db.Alert, int, error)
	MarkAlertsRead(ctx context.Context, user string, upTo int64) error
	Watch(ctx context.Context, user string, playerID int) error
	Unwatch(ctx context.Context, user string, playerID int) (int, error)
	Watchlist(ctx context.Context, user string) ([]int, error)
	EvalRuns(ctx context.Context, limit int) ([]db.EvalRun, error)
	BacktestRuns(ctx context.Context, model string, limit int) ([]db.BacktestRun, error)
	ListPlayers(ctx context.Context) ([]fpl.Player, error)
	ListTeams(ctx context.Context) ([]fpl.Team, error)
}

// WatchRunner starts and stops the background watch of a player. It is nil when no workflow
// server is configured: the watchlist is still kept, but nothing turns news into alerts.
type WatchRunner interface {
	Watch(ctx context.Context, playerID int, name string) error
	Unwatch(ctx context.Context, playerID int) error
}

// Inbox is the manager's alerts, watchlist, and the model-health history the background
// workflows record.
type Inbox struct {
	Store      InboxStore
	Watches    WatchRunner // nil without Temporal
	TemporalUI string      // the workflow server's web UI, empty without Temporal
	Now        func() time.Time
	// IsOwner says whether a user runs the site. Owners share one inbox and watchlist (and only
	// their alerts reach the phone); everyone else has their own. Nil treats everyone as an owner,
	// as on the owner's own computer.
	IsOwner func(user string) bool
}

// inbox is whose alerts and watchlist a user sees: "" for the owners' shared ones.
func (i *Inbox) inbox(user string) string {
	if i.IsOwner == nil || i.IsOwner(user) {
		return ""
	}
	return strings.ToLower(user)
}

// AlertView is one alert.
type AlertView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"` // "watch", "briefing", "health"
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	PlayerID int    `json:"player_id"`
	Age      string `json:"age"`
	Read     bool   `json:"read"`
}

// AlertsView is the inbox. Live says alerts are being produced (a workflow server is configured).
type AlertsView struct {
	Alerts     []AlertView `json:"alerts"`
	Unread     int         `json:"unread"`
	Live       bool        `json:"live"`
	TemporalUI string      `json:"temporal_ui"`
}

// WatchedView is one watched player.
type WatchedView struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Team   string `json:"team"`
	Status string `json:"status"`
}

// WatchlistView is everyone watched.
type WatchlistView struct {
	Players []WatchedView `json:"players"`
	Live    bool          `json:"live"`
}

// EvalRunView is one nightly eval.
type EvalRunView struct {
	Age      string   `json:"age"`
	Model    string   `json:"model"`
	PassRate float64  `json:"pass_rate"` // 0..1
	Failing  []string `json:"failing"`
}

// BacktestRunView is one backtest of the default model.
type BacktestRunView struct {
	Age       string  `json:"age"`
	ThroughGW int     `json:"through_gameweek"`
	Spearman  float64 `json:"spearman"`
	MAE       float64 `json:"mae"`
	Top20     float64 `json:"top20"`
}

// HealthView is the history of the nightly evals and of the default model's backtests, newest first.
type HealthView struct {
	Evals      []EvalRunView     `json:"evals"`
	Backtests  []BacktestRunView `json:"backtests"`
	Model      string            `json:"backtest_model"`
	Live       bool              `json:"live"`
	TemporalUI string            `json:"temporal_ui"`
}

const inboxLimit = 50

func (i *Inbox) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

// Alerts returns the newest alerts in the user's inbox.
func (i *Inbox) Alerts(ctx context.Context, user string) (AlertsView, error) {
	list, unread, err := i.Store.ListAlerts(ctx, i.inbox(user), inboxLimit)
	if err != nil {
		return AlertsView{}, err
	}
	v := AlertsView{Alerts: make([]AlertView, 0, len(list)), Unread: unread, Live: i.Watches != nil}
	if i.inbox(user) == "" {
		v.TemporalUI = i.TemporalUI // the Temporal UI is the owners' (the ingress lets only them in)
	}
	now := i.now()
	for _, a := range list {
		v.Alerts = append(v.Alerts, AlertView{ID: a.ID, Kind: a.Kind, Title: a.Title, Body: a.Body, URL: a.URL,
			PlayerID: a.PlayerID, Age: age(now, a.CreatedAt), Read: a.Read})
	}
	return v, nil
}

// MarkRead marks every alert in the user's inbox up to and including upTo as read.
func (i *Inbox) MarkRead(ctx context.Context, user string, upTo int64) error {
	if upTo < 1 {
		return badInput("up_to must be an alert id")
	}
	return i.Store.MarkAlertsRead(ctx, i.inbox(user), upTo)
}

// Watchlist returns the players the user watches.
func (i *Inbox) Watchlist(ctx context.Context, user string) (WatchlistView, error) {
	ids, err := i.Store.Watchlist(ctx, i.inbox(user))
	if err != nil {
		return WatchlistView{}, err
	}
	players, short, err := i.players(ctx)
	if err != nil {
		return WatchlistView{}, err
	}
	v := WatchlistView{Players: make([]WatchedView, 0, len(ids)), Live: i.Watches != nil}
	for _, id := range ids {
		if p, ok := players[id]; ok {
			v.Players = append(v.Players, WatchedView{ID: id, Name: p.WebName, Team: short[p.TeamID], Status: statusText(p)})
		}
	}
	return v, nil
}

// SetWatch adds a player to the user's watchlist or takes him off it. His background watch starts
// with his first watcher and stops when nobody watches him any more.
func (i *Inbox) SetWatch(ctx context.Context, user string, playerID int, on bool) (WatchlistView, error) {
	players, _, err := i.players(ctx)
	if err != nil {
		return WatchlistView{}, err
	}
	p, ok := players[playerID]
	if !ok {
		return WatchlistView{}, badInput("no player has id %d", playerID)
	}
	box := i.inbox(user)
	if on {
		if err := i.Store.Watch(ctx, box, playerID); err != nil {
			return WatchlistView{}, err
		}
		if i.Watches != nil { // starting a watch that is already running leaves it as it is
			if err := i.Watches.Watch(ctx, playerID, p.WebName); err != nil {
				return WatchlistView{}, err
			}
		}
	} else {
		left, err := i.Store.Unwatch(ctx, box, playerID)
		if err != nil {
			return WatchlistView{}, err
		}
		if i.Watches != nil && left == 0 {
			if err := i.Watches.Unwatch(ctx, playerID); err != nil {
				return WatchlistView{}, err
			}
		}
	}
	return i.Watchlist(ctx, user)
}

// Health returns the eval and backtest history.
func (i *Inbox) Health(ctx context.Context) (HealthView, error) {
	evals, err := i.Store.EvalRuns(ctx, 30)
	if err != nil {
		return HealthView{}, err
	}
	bts, err := i.Store.BacktestRuns(ctx, projection.DefaultModelName, 30)
	if err != nil {
		return HealthView{}, err
	}
	now := i.now()
	v := HealthView{Evals: make([]EvalRunView, 0, len(evals)), Backtests: make([]BacktestRunView, 0, len(bts)),
		Model: projection.DefaultModelName, Live: i.Watches != nil, TemporalUI: i.TemporalUI}
	for _, e := range evals {
		v.Evals = append(v.Evals, EvalRunView{Age: age(now, e.At), Model: e.Model, PassRate: math.Round(e.PassRate*1000) / 1000, Failing: e.Failing})
	}
	for _, b := range bts {
		v.Backtests = append(v.Backtests, BacktestRunView{Age: age(now, b.At), ThroughGW: b.ThroughGW,
			Spearman: math.Round(b.Spearman*1000) / 1000, MAE: round2(b.MAE), Top20: round2(b.Top20)})
	}
	return v, nil
}

func (i *Inbox) players(ctx context.Context) (map[int]fpl.Player, map[int]string, error) {
	ps, err := i.Store.ListPlayers(ctx)
	if err != nil {
		return nil, nil, err
	}
	teams, err := i.Store.ListTeams(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int]fpl.Player, len(ps))
	for _, p := range ps {
		byID[p.ID] = p
	}
	short := make(map[int]string, len(teams))
	for _, t := range teams {
		short[t.ID] = t.ShortName
	}
	return byID, short, nil
}
