package service_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

type inboxStore struct {
	alerts  []db.Alert
	readTo  map[string]int64
	watched map[string][]int // inbox -> players
	evals   []db.EvalRun
	bts     map[string][]db.BacktestRun
}

func newInboxStore() *inboxStore {
	return &inboxStore{readTo: map[string]int64{}, watched: map[string][]int{}}
}

func (s *inboxStore) ListAlerts(_ context.Context, user string, limit int) ([]db.Alert, int, error) {
	var out []db.Alert
	unread := 0
	for _, a := range s.alerts {
		if a.User != user {
			continue
		}
		if !a.Read {
			unread++
		}
		out = append(out, a)
	}
	return out[:min(limit, len(out))], unread, nil
}
func (s *inboxStore) MarkAlertsRead(_ context.Context, user string, upTo int64) error {
	s.readTo[user] = upTo
	return nil
}
func (s *inboxStore) Watch(_ context.Context, user string, id int) error {
	if !slices.Contains(s.watched[user], id) {
		s.watched[user] = append(s.watched[user], id)
	}
	return nil
}
func (s *inboxStore) Unwatch(_ context.Context, user string, id int) (int, error) {
	s.watched[user] = slices.DeleteFunc(s.watched[user], func(x int) bool { return x == id })
	left := 0
	for _, ids := range s.watched {
		if slices.Contains(ids, id) {
			left++
		}
	}
	return left, nil
}
func (s *inboxStore) Watchlist(_ context.Context, user string) ([]int, error) {
	return slices.Clone(s.watched[user]), nil
}
func (s *inboxStore) EvalRuns(context.Context, int) ([]db.EvalRun, error) { return s.evals, nil }
func (s *inboxStore) BacktestRuns(_ context.Context, model string, _ int) ([]db.BacktestRun, error) {
	return s.bts[model], nil
}
func (s *inboxStore) ListPlayers(context.Context) ([]fpl.Player, error) {
	return []fpl.Player{{ID: 4, WebName: "Palmer", TeamID: 2, Status: "s"}, {ID: 7, WebName: "Saka", TeamID: 1, Status: "a"}}, nil
}
func (s *inboxStore) ListTeams(context.Context) ([]fpl.Team, error) {
	return []fpl.Team{{ID: 1, ShortName: "ARS"}, {ID: 2, ShortName: "CHE"}}, nil
}

type runner struct {
	calls []string
	err   error
}

func (r *runner) Watch(_ context.Context, id int, name string) error {
	r.calls = append(r.calls, "watch "+name)
	return r.err
}
func (r *runner) Unwatch(_ context.Context, id int) error {
	r.calls = append(r.calls, "unwatch")
	return r.err
}

var inboxNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func owners(user string) bool { return user == "owner@example.com" || user == "local" }

func TestInbox_AlertsArePerUserAndOwnersShareOne(t *testing.T) {
	st := newInboxStore()
	st.alerts = []db.Alert{
		{ID: 2, Kind: "watch", Title: "Palmer: suspended", PlayerID: 4, CreatedAt: inboxNow.Add(-2 * time.Hour)},
		{ID: 1, Kind: "health", Title: "Eval at 80%", CreatedAt: inboxNow.Add(-26 * time.Hour), Read: true},
		{ID: 3, User: "visitor@example.com", Kind: "watch", Title: "Saka in the news", CreatedAt: inboxNow},
	}
	in := &service.Inbox{Store: st, Watches: &runner{}, IsOwner: owners, Now: func() time.Time { return inboxNow }}
	ctx := context.Background()

	v, err := in.Alerts(ctx, "owner@example.com")
	if err != nil || v.Unread != 1 || len(v.Alerts) != 2 || !v.Live || v.Alerts[0].Title != "Palmer: suspended" || v.Alerts[0].Age != "2h" {
		t.Fatalf("owner: %+v %v", v, err)
	}
	mine, _ := in.Alerts(ctx, "Visitor@Example.com")
	if len(mine.Alerts) != 1 || mine.Alerts[0].Title != "Saka in the news" {
		t.Errorf("a visitor sees only their own: %+v", mine.Alerts)
	}
	if none, _ := in.Alerts(ctx, "someone@example.com"); len(none.Alerts) != 0 {
		t.Errorf("a new visitor has an empty inbox: %+v", none.Alerts)
	}

	if err := in.MarkRead(ctx, "visitor@example.com", 3); err != nil || st.readTo["visitor@example.com"] != 3 || st.readTo[""] != 0 {
		t.Errorf("mark read reaches only the visitor's inbox: %v %v", err, st.readTo)
	}
	var ie *service.InputError
	if err := in.MarkRead(ctx, "owner@example.com", 0); !errors.As(err, &ie) {
		t.Errorf("mark read 0: %v", err)
	}
}

func TestInbox_WatchesArePerUserAndTheWatchStopsWithTheLastWatcher(t *testing.T) {
	ctx := context.Background()
	st, r := newInboxStore(), &runner{}
	in := &service.Inbox{Store: st, Watches: r, IsOwner: owners}

	v, err := in.SetWatch(ctx, "owner@example.com", 4, true)
	if err != nil || len(v.Players) != 1 || v.Players[0].Name != "Palmer" || v.Players[0].Team != "CHE" || v.Players[0].Status != "suspended" || !v.Live {
		t.Fatalf("owner watches: %+v %v", v, err)
	}
	if _, err := in.SetWatch(ctx, "visitor@example.com", 4, true); err != nil {
		t.Fatal(err)
	}
	if mine, _ := in.Watchlist(ctx, "visitor@example.com"); len(mine.Players) != 1 {
		t.Errorf("the visitor's own list: %+v", mine)
	}
	if theirs, _ := in.Watchlist(ctx, "other@example.com"); len(theirs.Players) != 0 {
		t.Errorf("another visitor sees none of it: %+v", theirs)
	}

	_, _ = in.SetWatch(ctx, "owner@example.com", 4, false)
	if slices.Contains(r.calls, "unwatch") {
		t.Error("the visitor still watches him, so his watch keeps running")
	}
	_, _ = in.SetWatch(ctx, "visitor@example.com", 4, false)
	if !slices.Equal(r.calls, []string{"watch Palmer", "watch Palmer", "unwatch"}) {
		t.Errorf("calls %v: the watch stops with the last watcher", r.calls)
	}

	var ie *service.InputError
	if _, err := in.SetWatch(ctx, "owner@example.com", 999, true); !errors.As(err, &ie) {
		t.Errorf("unknown player: %v", err)
	}
	plain := &service.Inbox{Store: newInboxStore()} // no Temporal, no owner rule: the local user
	if v, err := plain.SetWatch(ctx, "local", 7, true); err != nil || len(v.Players) != 1 || v.Live {
		t.Errorf("without Temporal: %+v %v", v, err)
	}
	failing := &service.Inbox{Store: newInboxStore(), Watches: &runner{err: errors.New("temporal down")}}
	if _, err := failing.SetWatch(ctx, "local", 7, true); err == nil {
		t.Error("a watch that could not start is an error")
	}
}

func TestInbox_Health(t *testing.T) {
	st := &inboxStore{
		readTo: map[string]int64{}, watched: map[string][]int{},
		evals: []db.EvalRun{{At: inboxNow.Add(-9 * time.Hour), Model: "haiku", PassRate: 0.9583, Failing: []string{"captain"}}},
		bts: map[string][]db.BacktestRun{
			projection.DefaultModelName: {{At: inboxNow.Add(-50 * time.Hour), ThroughGW: 7, Spearman: 0.41234, MAE: 1.876, Top20: 5.123}},
			"form":                      {{ThroughGW: 7}},
		},
	}
	in := &service.Inbox{Store: st, TemporalUI: "http://localhost:8233", Now: func() time.Time { return inboxNow }}

	v, err := in.Health(context.Background())

	if err != nil || len(v.Evals) != 1 || len(v.Backtests) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	if e := v.Evals[0]; e.PassRate != 0.958 || e.Age != "9h" || e.Failing[0] != "captain" {
		t.Errorf("eval = %+v", e)
	}
	if b := v.Backtests[0]; b.Spearman != 0.412 || b.MAE != 1.88 || b.ThroughGW != 7 || v.Model != projection.DefaultModelName || v.TemporalUI == "" {
		t.Errorf("backtest = %+v (%+v)", b, v)
	}
}
