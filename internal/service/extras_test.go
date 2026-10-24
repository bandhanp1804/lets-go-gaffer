package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func withDeadline(t time.Time) func(*world) {
	return func(w *world) {
		for i := range w.events {
			if w.events[i].ID == 8 {
				w.events[i].Name = "Gameweek 8"
				w.events[i].DeadlineTime = t.Format(time.RFC3339)
			}
		}
	}
}

func TestStatus(t *testing.T) {
	r := newRig(t, rigOpts{mutate: withDeadline(testNow.Add(50*time.Hour + 20*time.Minute))})

	got, err := r.svc.Status(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.Gameweek != 8 || got.GameweekName != "Gameweek 8" || got.LastFinished != 7 || got.HistoryThrough != 7 {
		t.Errorf("season = %+v", got)
	}
	if got.DeadlineIn != "2d 2h" {
		t.Errorf("deadline in = %q, want 2d 2h", got.DeadlineIn)
	}
	if got.Players != 45 || len(got.Clubs) != 12 || got.Clubs[0] != (service.ClubView{Short: "C01", Name: "Club1"}) {
		t.Errorf("players %d clubs %v", got.Players, got.Clubs)
	}
	if got.NewsArticles != 12 || !got.NewsSearch || !got.TeamConfigured {
		t.Errorf("news %d search %v team %v", got.NewsArticles, got.NewsSearch, got.TeamConfigured)
	}
	if got.Session.State != "not configured" || got.Session.Configured {
		t.Errorf("session = %+v", got.Session)
	}
	if got.Notes == nil {
		t.Error("notes must be an empty list, not null")
	}
}

func TestStatus_DeadlinePassedAndNoEmbedder(t *testing.T) {
	r := newRig(t, rigOpts{noEmbed: true, mutate: withDeadline(testNow.Add(-time.Minute))})

	got, err := r.svc.Status(context.Background())

	if err != nil || got.DeadlineIn != "passed" || got.NewsSearch {
		t.Errorf("err %v, %+v", err, got)
	}
}

func TestStatus_Session(t *testing.T) {
	tests := []struct {
		name, session, state, expires string
	}{
		{"valid", token(testNow.Add(47 * time.Minute)), "valid", "47 min"},
		{"expired", token(testNow.Add(-time.Hour)), "expired", ""},
		{"not a token", "Bearer opaque", "unknown expiry", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, rigOpts{live: &fakeLive{}, session: tt.session})

			got, err := r.svc.Status(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			if !got.Session.Configured || got.Session.State != tt.state || got.Session.ExpiresIn != tt.expires {
				t.Errorf("session = %+v", got.Session)
			}
		})
	}
}

func TestStatus_NeverContactsLiveFPLOrLeaksTheToken(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(time.Hour))})

	got, _ := r.svc.Status(context.Background())

	if live.calls != 0 {
		t.Errorf("the dashboard called live FPL %d times", live.calls)
	}
	if b, _ := jsonOf(got); contains(b, "SECRET") {
		t.Errorf("token material in the status: %s", b)
	}
}

func TestBuildSquad_IsLegalWithinBudgetAndPicksTheStrongPlayers(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{})

	if err != nil {
		t.Fatal(err)
	}
	if got.Gameweek != 8 || got.Horizon != 4 || got.Budget != "£100.0m" {
		t.Errorf("header = %+v", got)
	}
	if len(got.Starting) != 11 || len(got.Bench) != 4 {
		t.Fatalf("starting %d bench %d", len(got.Starting), len(got.Bench))
	}
	all := append(append([]service.PlayerView{}, got.Starting...), got.Bench...)
	perClub := map[string]int{}
	for _, p := range all {
		perClub[p.Team]++
		if perClub[p.Team] > 3 {
			t.Errorf("more than three players from %s", p.Team)
		}
	}
	if !contains(names(got.Starting), "Mkt00") { // the 9-a-week goalkeeper is the obvious pick
		t.Errorf("starting XI = %v, want the strong goalkeeper Mkt00", names(got.Starting))
	}
	if got.Captain == "" || got.ViceCaptain == "" || got.Captain == got.ViceCaptain || got.Formation == "" || got.ExpectedPoints <= 0 {
		t.Errorf("captain %q vice %q formation %q xp %v", got.Captain, got.ViceCaptain, got.Formation, got.ExpectedPoints)
	}
	if got.Cost > got.Budget && len(got.Cost) >= len(got.Budget) {
		t.Errorf("cost %s over budget %s", got.Cost, got.Budget)
	}
}

func TestBuildSquad_BadHorizonIsAnInputError(t *testing.T) {
	r := newRig(t, rigOpts{})
	for _, h := range []int{-1, 11} {
		_, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{Horizon: h})
		var in *service.InputError
		if !errors.As(err, &in) {
			t.Errorf("horizon %d: err = %v, want an InputError", h, err)
		}
	}
}

func TestBuildSquad_DoesNotNeedTheManagersTeam(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.entryErr = errors.New("FPL is down")

	if _, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{Horizon: 2}); err != nil {
		t.Errorf("the squad builder must not depend on the manager's squad: %v", err)
	}
}

func TestBacktest(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Backtest(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.FirstGameweek != 3 || got.LastGameweek != 7 || len(got.Models) != 7 || len(got.PerGameweek) != 5 || len(got.WalkForward) != 3 {
		t.Errorf("shape = %d-%d models %d per-gw %d walk %d", got.FirstGameweek, got.LastGameweek, len(got.Models), len(got.PerGameweek), len(got.WalkForward))
	}
	if got.WalkForward[0].TunedOn != "GW3-4" || got.Default.RateDecay == 0 {
		t.Errorf("walk-forward %+v default %+v", got.WalkForward[0], got.Default)
	}
	if !hasNote(got.Notes, "Only 5 gameweeks") {
		t.Errorf("a short history must be flagged: %v", got.Notes)
	}
}

func TestBacktest_IsKeptUntilTheHistoryChanges(t *testing.T) {
	r := newRig(t, rigOpts{})
	first, err := r.svc.Backtest(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	*r.now = r.now.Add(10 * time.Minute) // past the snapshot TTL: the data is re-read, unchanged
	again, err := r.svc.Backtest(context.Background())
	if err != nil || again.LastGameweek != first.LastGameweek || len(again.Models) != len(first.Models) {
		t.Fatalf("same history, different answer: %v %+v", err, again)
	}

	for _, p := range r.store.w.players { // GW8 is played
		r.store.w.history = append(r.store.w.history, fpl.PlayerHistory{PlayerID: p.ID, GameweekID: 8, Fixture: p.ID*10 + 8, TotalPoints: 3, Minutes: 90})
	}
	*r.now = r.now.Add(10 * time.Minute)
	updated, err := r.svc.Backtest(context.Background())
	if err != nil || updated.LastGameweek != 8 {
		t.Errorf("new history must refresh the backtest: %v last=%d", err, updated.LastGameweek)
	}
}

func TestBacktest_TooLittleHistory(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		var keep []fpl.PlayerHistory
		for _, h := range w.history {
			if h.GameweekID <= 2 {
				keep = append(keep, h)
			}
		}
		w.history = keep
	}})

	_, err := r.svc.Backtest(context.Background())

	if err == nil {
		t.Fatal("two gameweeks cannot be backtested")
	}
}

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func contains[T comparable](xs any, want T) bool {
	switch v := xs.(type) {
	case string:
		w, _ := any(want).(string)
		return strings.Contains(v, w)
	case []T:
		return slices.Contains(v, want)
	}
	return false
}
