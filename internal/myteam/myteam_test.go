package myteam_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

var errAPI = errors.New("fpl is down")

// fakeClient serves a canned entry and picks and records what was asked for.
type fakeClient struct {
	entry     *fpl.Entry
	picks     *fpl.Picks
	entryErr  error
	picksErr  error
	gotTeam   int
	gotEvent  int
	picksCall int
}

func (f *fakeClient) FetchEntry(ctx context.Context, id int) (*fpl.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.gotTeam = id
	return f.entry, f.entryErr
}

func (f *fakeClient) FetchPicks(ctx context.Context, id, gw int) (*fpl.Picks, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.picksCall++
	f.gotEvent = gw
	return f.picks, f.picksErr
}

// position for pick slot p: 1 GKP | 2-5 DEF | 6-9 MID | 10-11 FWD | bench 12 GKP, 13 DEF, 14 MID, 15 FWD.
func slotPosition(p int) fpl.Position {
	switch {
	case p == 1 || p == 12:
		return fpl.GKP
	case p <= 5 || p == 13:
		return fpl.DEF
	case p <= 9 || p == 14:
		return fpl.MID
	}
	return fpl.FWD
}

// fixture builds a 4-4-2 team. Slot p holds player 100+p; clubs rotate so that
// each of five clubs has exactly three players. Every player costs 7.0m, so the
// squad is worth £105.0m: over the standard budget, as a real squad can be once
// prices rise.
func fixture() (*fakeClient, []fpl.Player) {
	var players []fpl.Player
	var picks []fpl.Pick
	for p := 1; p <= 15; p++ {
		players = append(players, fpl.Player{
			ID: 100 + p, WebName: "P" + string(rune('A'+p-1)), TeamID: (p-1)%5 + 1,
			Position: slotPosition(p), NowCost: 70,
		})
		picks = append(picks, fpl.Pick{
			Element: 100 + p, Position: p, Multiplier: 1, ElementType: slotPosition(p),
			IsCaptain: p == 10, IsViceCaptain: p == 6,
		})
	}
	picks[9].Multiplier = 2
	chip := "wildcard"
	return &fakeClient{
		entry: &fpl.Entry{ID: 4321, Name: "Gaffer FC", FirstName: "Pat", LastName: "Doe", CurrentEvent: 7},
		picks: &fpl.Picks{ActiveChip: &chip, Picks: picks, EntryHistory: fpl.EntryHistory{Event: 7, Points: 69, Bank: 1, Value: 1050}},
	}, players
}

func TestLoad_BuildsTheSquadFromPicksAndStoredPlayers(t *testing.T) {
	c, players := fixture()

	team, err := myteam.Load(context.Background(), c, 4321, players)

	if err != nil {
		t.Fatal(err)
	}
	if c.gotTeam != 4321 || c.gotEvent != 7 {
		t.Errorf("asked for team %d gameweek %d, want 4321 and 7 (the entry's current gameweek)", c.gotTeam, c.gotEvent)
	}
	if team.Entry.Name != "Gaffer FC" || team.Event != 7 || team.Chip != "wildcard" {
		t.Errorf("team = %+v", team)
	}
	if team.History.Bank != 1 || team.History.Points != 69 {
		t.Errorf("history = %+v", team.History)
	}

	// Squad order is pick order: the XI first, then the bench.
	var ids []int
	for _, p := range team.Squad.Players {
		ids = append(ids, p.ID)
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115}; !slices.Equal(ids, want) {
		t.Errorf("players = %v, want pick order %v", ids, want)
	}
	if team.Squad.Players[0].NowCost != 70 || team.Squad.Players[0].WebName != "PA" {
		t.Errorf("player data should come from the stored player: %+v", team.Squad.Players[0])
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111}; !slices.Equal(team.Squad.Starting, want) {
		t.Errorf("starting = %v, want slots 1-11 %v", team.Squad.Starting, want)
	}
	if team.Squad.Captain != 110 || team.Squad.ViceCaptain != 106 {
		t.Errorf("captain/vice = %d/%d, want 110/106", team.Squad.Captain, team.Squad.ViceCaptain)
	}
}

func TestLoad_ProducesASquadTheRulesAccept(t *testing.T) {
	c, players := fixture()

	team, err := myteam.Load(context.Background(), c, 4321, players)
	if err != nil {
		t.Fatal(err)
	}

	// Every rule except the budget: this squad is worth 105.0m, legal for its owner.
	if err := optimizer.ValidateWithBudget(team.Squad, optimizer.NoBudgetLimit); err != nil {
		t.Errorf("a real squad was rejected, so the rule model disagrees with FPL: %v", err)
	}
}

func TestLoad_NoChipIsAnEmptyString(t *testing.T) {
	c, players := fixture()
	c.picks.ActiveChip = nil

	team, err := myteam.Load(context.Background(), c, 4321, players)

	if err != nil || team.Chip != "" {
		t.Errorf("chip = %q, err = %v; want empty", team.Chip, err)
	}
}

func TestLoad_UsesTheEntrysCurrentGameweek(t *testing.T) {
	c, players := fixture()
	c.entry.CurrentEvent = 12

	if _, err := myteam.Load(context.Background(), c, 4321, players); err != nil {
		t.Fatal(err)
	}

	if c.gotEvent != 12 {
		t.Errorf("fetched gameweek %d, want 12", c.gotEvent)
	}
}

func TestLoad_SortsPicksByPositionWhateverTheResponseOrder(t *testing.T) {
	c, players := fixture()
	slices.Reverse(c.picks.Picks)

	team, err := myteam.Load(context.Background(), c, 4321, players)

	if err != nil {
		t.Fatal(err)
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111}; !slices.Equal(team.Squad.Starting, want) {
		t.Errorf("starting = %v, want the XI by position %v", team.Squad.Starting, want)
	}
}

func TestLoad_Failures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *fakeClient, players *[]fpl.Player)
		wantIs  error
		wantMsg string
	}{
		{"entry fetch fails", func(c *fakeClient, _ *[]fpl.Player) { c.entryErr = errAPI }, errAPI, "4321"},
		{"picks fetch fails", func(c *fakeClient, _ *[]fpl.Player) { c.picksErr = errAPI }, errAPI, "gameweek 7"},
		{"team has not played a gameweek", func(c *fakeClient, _ *[]fpl.Player) { c.entry.CurrentEvent = 0 }, nil, "no gameweek"},
		{"stored players are stale", func(_ *fakeClient, p *[]fpl.Player) { *p = slices.Delete(*p, 4, 5) }, nil, "105"},
		{"fewer than 15 picks", func(c *fakeClient, _ *[]fpl.Player) { c.picks.Picks = c.picks.Picks[:14] }, nil, "14"},
		{"no captain in the picks", func(c *fakeClient, _ *[]fpl.Player) { c.picks.Picks[9].IsCaptain = false }, nil, "captain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, players := fixture()
			tc.mutate(c, &players)

			_, err := myteam.Load(context.Background(), c, 4321, players)

			if err == nil {
				t.Fatal("want an error")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantIs)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

func TestLoad_AnUnknownPlayerErrorTellsTheUserHowToFixIt(t *testing.T) {
	c, players := fixture()
	players = slices.Delete(players, 4, 5) // 105 is, say, a new signing

	_, err := myteam.Load(context.Background(), c, 4321, players)

	if err == nil || !strings.Contains(err.Error(), "gaffer sync") {
		t.Errorf("err = %v, want advice to run gaffer sync", err)
	}
}

func TestLoad_CancelledContext(t *testing.T) {
	c, players := fixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := myteam.Load(ctx, c, 4321, players)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
