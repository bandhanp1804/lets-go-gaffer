package workflows_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

// store fakes the parts of the store a test uses; anything else panics through the nil interface.
type store struct {
	workflows.Store
	states   map[int]db.PlayerState
	watched  []int
	upserted []fpl.Player
	claimed  map[int]bool
	released []int
	prev     []db.BacktestRun
	saved    []db.BacktestRun
}

func (s *store) PlayerStates(context.Context) (map[int]db.PlayerState, error) { return s.states, nil }
func (s *store) Watchlist(context.Context) ([]int, error)                     { return s.watched, nil }
func (s *store) UpsertTeams(context.Context, []fpl.Team) error                { return nil }
func (s *store) UpsertPlayers(_ context.Context, ps []fpl.Player) error {
	s.upserted = ps
	return nil
}
func (s *store) MarkBriefingSent(_ context.Context, gw int) (bool, error) {
	if s.claimed[gw] {
		return false, nil
	}
	s.claimed[gw] = true
	return true, nil
}
func (s *store) ReleaseBriefing(_ context.Context, gw int) error {
	delete(s.claimed, gw)
	s.released = append(s.released, gw)
	return nil
}
func (s *store) BacktestRuns(context.Context, string, int) ([]db.BacktestRun, error) {
	return s.prev, nil
}
func (s *store) SaveBacktestRuns(_ context.Context, r []db.BacktestRun) error {
	s.saved = r
	return nil
}

type fplFake struct {
	workflows.FPL
	boot *fpl.Bootstrap
}

func (f fplFake) FetchBootstrap(context.Context) (*fpl.Bootstrap, error) { return f.boot, nil }

func pl(id int, name, status, news string) fpl.Player {
	return fpl.Player{ID: id, WebName: name, TeamID: 1, Status: status, News: news}
}

func TestRefreshPlayers_ReportsOnlyWatchedPlayersWhoseStatusOrNoteChanged(t *testing.T) {
	added := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	saka := pl(7, "Saka", "d", "Knock - 75% chance of playing")
	saka.NewsAdded = &added
	boot := &fpl.Bootstrap{Teams: []fpl.Team{{ID: 1, Name: "Arsenal"}}, Players: []fpl.Player{
		saka,
		pl(8, "Rice", "a", ""),                  // watched, unchanged
		pl(9, "Palmer", "s", "Suspended"),       // changed, but not watched
		pl(10, "Isak", "i", "Groin"),            // watched, but new to the store: nothing to compare
		pl(11, "Wood", "a", "Back in training"), // watched, note changed
	}}
	st := &store{
		states:  map[int]db.PlayerState{7: {Status: "a"}, 8: {Status: "a"}, 9: {Status: "a"}, 11: {Status: "a", News: "Knee"}},
		watched: []int{7, 8, 10, 11},
	}
	act := &workflows.Activities{Store: st, FPL: fplFake{boot: boot}}

	got, err := act.RefreshPlayers(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(st.upserted) != 5 {
		t.Errorf("all players stored: %d", len(st.upserted))
	}
	if len(got.Changes) != 2 || got.Changes[0].Name != "Saka" || got.Changes[0].Status != "doubtful" || got.Changes[1].Name != "Wood" {
		t.Errorf("changes = %+v", got.Changes)
	}
	if len(got.FPLItems) != 1 || !strings.Contains(got.FPLItems[0].Title, "Saka") {
		t.Errorf("FPL notes as items: %+v", got.FPLItems)
	}
}

type advisor struct {
	workflows.Advisor
	err error
}

func (a advisor) LineupWith(context.Context, service.LineupRequest) (service.LineupView, error) {
	return service.LineupView{Captain: "Haaland", ViceCaptain: "Saka", Formation: "3-4-3", ExpectedPoints: 61.25}, a.err
}
func (advisor) Transfers(context.Context, service.TransfersRequest) (service.TransfersView, error) {
	return service.TransfersView{}, nil
}
func (advisor) ChipsFor(context.Context, service.ChipsRequest) (service.ChipsView, error) {
	return service.ChipsView{}, nil
}

type notifier struct {
	got []notify.Message
	err error
}

func (n *notifier) Notify(_ context.Context, m notify.Message) error {
	if n.err != nil {
		return n.err
	}
	n.got = append(n.got, m)
	return nil
}

func TestSendBriefing_SendsOncePerGameweekAndReleasesOnFailure(t *testing.T) {
	ctx := context.Background()
	deadline := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	st := &store{claimed: map[int]bool{}}
	n := &notifier{}
	act := &workflows.Activities{Store: st, Notifier: n, Advisor: advisor{}}

	if err := act.SendBriefing(ctx, 9, deadline); err != nil {
		t.Fatal(err)
	}
	if err := act.SendBriefing(ctx, 9, deadline); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 1 || n.got[0].Title != "GW9 briefing: captain Haaland" {
		t.Fatalf("sent %+v, want one briefing", n.got)
	}

	n.err = errors.New("db down")
	if err := act.SendBriefing(ctx, 10, deadline); err == nil {
		t.Fatal("a failed send is an error, so Temporal retries it")
	}
	if len(st.released) != 1 || st.claimed[10] {
		t.Errorf("the claim on GW10 should be released for the retry: released %v", st.released)
	}

	act.Advisor = advisor{err: service.ErrTeamRequired}
	n.err = nil
	err := act.SendBriefing(ctx, 11, deadline)
	if err == nil || !strings.Contains(err.Error(), "GAFFER_TEAM_ID") {
		t.Errorf("no team configured: err = %v", err)
	}
}

func TestBriefing_ArrangesTheComputedNumbers(t *testing.T) {
	deadline := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	l := service.LineupView{Captain: "Haaland", ViceCaptain: "Belloumi", Formation: "4-4-2", ExpectedPoints: 61.3,
		Starting: []service.PlayerView{{Name: "Saka", Status: "doubtful (50%)"}, {Name: "Rice", Status: "available"}},
		Bench:    []service.PlayerView{{Name: "Hall", Status: "injured"}}}
	move := service.MoveView{Out: service.PlayerView{Name: "Hughes"}, In: service.PlayerView{Name: "Gomez"}}
	tests := []struct {
		name string
		t    service.TransfersView
		c    service.ChipsView
		want []string
		not  []string
	}{
		{"one move, no chip",
			service.TransfersView{Horizon: 4, Suggestions: []service.SuggestionView{{Moves: []service.MoveView{move}, NetPoints: 4.4}}},
			service.ChipsView{Chips: []service.ChipAdviceView{{Name: "Bench Boost", Available: true, Verdict: "wait"}}},
			[]string{"Captain Haaland, vice Belloumi.", "Best XI: 4-4-2, 61.3 expected points.", "Check: Saka doubtful (50%), Hall injured.", "Transfers: Hughes → Gomez (+4.4 net over 4 gameweeks)."},
			[]string{"Rice", "Chip:", "hit"}},
		{"a hit and a chip to play",
			service.TransfersView{Horizon: 4, Suggestions: []service.SuggestionView{{Moves: []service.MoveView{move, move}, NetPoints: 1.2, Hit: 4}}},
			service.ChipsView{Chips: []service.ChipAdviceView{{Name: "Triple Captain", Available: true, Verdict: "good_week_now", Headline: "Haaland has a double."}}},
			[]string{"a 4-point hit", "Chip: Triple Captain. Haaland has a double."}, nil},
		{"keep the squad", service.TransfersView{}, service.ChipsView{}, []string{"Transfers: keep the squad as it is."}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := workflows.Briefing(9, deadline, l, tt.t, tt.c)
			if m.Kind != "briefing" || m.Title != "GW9 briefing: captain Haaland" {
				t.Errorf("message = %+v", m)
			}
			for _, w := range tt.want {
				if !strings.Contains(m.Body, w) {
					t.Errorf("body lacks %q:\n%s", w, m.Body)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(m.Body, w) {
					t.Errorf("body should not say %q:\n%s", w, m.Body)
				}
			}
		})
	}
}

func TestBacktestOutcome_Dropped(t *testing.T) {
	tests := []struct {
		o    workflows.BacktestOutcome
		want bool
	}{
		{workflows.BacktestOutcome{Spearman: 0.36, Previous: 0.42, HadPrior: true}, true},
		{workflows.BacktestOutcome{Spearman: 0.38, Previous: 0.42, HadPrior: true}, false},
		{workflows.BacktestOutcome{Spearman: 0.50, Previous: 0.42, HadPrior: true}, false},
		{workflows.BacktestOutcome{Spearman: 0.10}, false}, // nothing to compare with
	}
	for _, tt := range tests {
		if got := tt.o.Dropped(); got != tt.want {
			t.Errorf("%+v: %v, want %v", tt.o, got, tt.want)
		}
	}
}
