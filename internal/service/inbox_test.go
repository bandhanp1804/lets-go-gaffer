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
	readTo  int64
	watched []int
	evals   []db.EvalRun
	bts     map[string][]db.BacktestRun
}

func (s *inboxStore) ListAlerts(_ context.Context, limit int) ([]db.Alert, int, error) {
	unread := 0
	for _, a := range s.alerts {
		if !a.Read {
			unread++
		}
	}
	return s.alerts[:min(limit, len(s.alerts))], unread, nil
}
func (s *inboxStore) MarkAlertsRead(_ context.Context, upTo int64) error { s.readTo = upTo; return nil }
func (s *inboxStore) Watch(_ context.Context, id int) error {
	if !slices.Contains(s.watched, id) {
		s.watched = append(s.watched, id)
	}
	return nil
}
func (s *inboxStore) Unwatch(_ context.Context, id int) error {
	s.watched = slices.DeleteFunc(s.watched, func(x int) bool { return x == id })
	return nil
}
func (s *inboxStore) Watchlist(context.Context) ([]int, error)            { return slices.Clone(s.watched), nil }
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

func TestInbox_Alerts(t *testing.T) {
	st := &inboxStore{alerts: []db.Alert{
		{ID: 2, Kind: "watch", Title: "Palmer: suspended", PlayerID: 4, CreatedAt: inboxNow.Add(-2 * time.Hour)},
		{ID: 1, Kind: "health", Title: "Eval at 80%", CreatedAt: inboxNow.Add(-26 * time.Hour), Read: true},
	}}
	tests := []struct {
		name    string
		watches service.WatchRunner
		live    bool
	}{
		{"with a workflow server", &runner{}, true},
		{"without one", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := &service.Inbox{Store: st, Watches: tt.watches, Now: func() time.Time { return inboxNow }}
			v, err := in.Alerts(context.Background())
			if err != nil || v.Unread != 1 || len(v.Alerts) != 2 || v.Live != tt.live {
				t.Fatalf("%+v %v", v, err)
			}
			if a := v.Alerts[0]; a.Title != "Palmer: suspended" || a.Age != "2h" || a.PlayerID != 4 || a.Read {
				t.Errorf("first = %+v", a)
			}
		})
	}
	in := &service.Inbox{Store: st}
	if err := in.MarkRead(context.Background(), 2); err != nil || st.readTo != 2 {
		t.Errorf("mark read: %v, %d", err, st.readTo)
	}
	var ie *service.InputError
	if err := in.MarkRead(context.Background(), 0); !errors.As(err, &ie) {
		t.Errorf("mark read 0: %v", err)
	}
}

func TestInbox_SetWatchKeepsTheListAndStartsOrStopsTheWatch(t *testing.T) {
	ctx := context.Background()
	st, r := &inboxStore{}, &runner{}
	in := &service.Inbox{Store: st, Watches: r}

	v, err := in.SetWatch(ctx, 4, true)
	if err != nil || len(v.Players) != 1 || v.Players[0].Name != "Palmer" || v.Players[0].Team != "CHE" || v.Players[0].Status != "suspended" || !v.Live {
		t.Fatalf("watch: %+v %v", v, err)
	}
	if _, err := in.SetWatch(ctx, 4, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.calls, []string{"watch Palmer", "unwatch"}) || len(st.watched) != 0 {
		t.Errorf("calls %v watched %v", r.calls, st.watched)
	}

	var ie *service.InputError
	if _, err := in.SetWatch(ctx, 999, true); !errors.As(err, &ie) {
		t.Errorf("unknown player: %v", err)
	}

	// Without a workflow server the list is still kept.
	plain := &service.Inbox{Store: &inboxStore{}}
	if v, err := plain.SetWatch(ctx, 7, true); err != nil || len(v.Players) != 1 || v.Live {
		t.Errorf("without Temporal: %+v %v", v, err)
	}

	// If the watch cannot start, he is on the list but the error says so.
	failing := &service.Inbox{Store: &inboxStore{}, Watches: &runner{err: errors.New("temporal down")}}
	if _, err := failing.SetWatch(ctx, 7, true); err == nil {
		t.Error("a watch that could not start is an error")
	}
}

func TestInbox_Health(t *testing.T) {
	st := &inboxStore{
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
