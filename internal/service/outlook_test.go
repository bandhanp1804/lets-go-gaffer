package service_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// aheadFixtures extends the test season: GW9 an ordinary week, GW10 a double for club 6 (the home
// of the strong goalkeeper Mkt00), and GW11 a blank for clubs 6 and 7. Every match has difficulty 3.
func aheadFixtures(w *world) {
	fx := func(id, ev, home, away int) {
		w.fixtures = append(w.fixtures, fpl.Fixture{ID: id, Event: ev, HomeTeam: home, AwayTeam: away, HomeDifficulty: 3, AwayDifficulty: 3})
	}
	fx(101, 9, 6, 7)
	fx(102, 9, 1, 2)
	fx(103, 10, 6, 7) // club 6 plays twice in GW10
	fx(104, 10, 8, 6)
	fx(105, 11, 1, 2) // clubs 6 and 7 have no match in GW11
	fx(106, 11, 3, 4)
}

func outlook(t *testing.T, r *rig, req service.OutlookRequest) service.OutlookView {
	t.Helper()
	v, err := r.svc.Outlook(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOutlook_ReadsTheFixtureListAheadIncludingDoublesAndBlanks(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})

	v := outlook(t, r, service.OutlookRequest{Players: []string{"Mkt00"}, Gameweeks: 4})

	if v.FirstGameweek != 8 || v.Gameweeks != 4 || len(v.Players) != 1 {
		t.Fatalf("view = %+v", v)
	}
	p := v.Players[0]
	if p.Name != "Mkt00" || p.Team != "C06" || p.Position != "GKP" || p.Owned {
		t.Errorf("player = %+v", p)
	}
	byGW := map[int]service.OutlookWeekView{}
	for _, w := range p.Weeks {
		byGW[w.Gameweek] = w
	}
	if len(byGW[8].Fixtures) != 1 || len(byGW[10].Fixtures) != 2 || len(byGW[11].Fixtures) != 0 {
		t.Errorf("fixtures: GW8 %v, GW10 %v, GW11 %v", byGW[8].Fixtures, byGW[10].Fixtures, byGW[11].Fixtures)
	}
	if byGW[11].XP != 0 {
		t.Errorf("a blank gameweek is worth nothing, got %v", byGW[11].XP)
	}
	// Same difficulty everywhere, so a double is worth twice a single week (to within rounding).
	if math.Abs(byGW[10].XP-2*byGW[9].XP) > 0.11 {
		t.Errorf("a double (%v) should be about twice a single week (%v)", byGW[10].XP, byGW[9].XP)
	}
	if p.BestGameweek != 10 || p.BestXP != byGW[10].XP || len(p.BestTied) != 0 {
		t.Errorf("best %d (%v), tied %v: the double should stand alone", p.BestGameweek, p.BestXP, p.BestTied)
	}
	if len(byGW[8].Difficulty) != 1 || byGW[8].Difficulty[0] != 3 || len(byGW[10].Difficulty) != 2 {
		t.Errorf("difficulty = %v / %v", byGW[8].Difficulty, byGW[10].Difficulty)
	}
}

func TestOutlook_CaptainFiguresMultiplyOutExactlyFromWhatIsShown(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})

	v := outlook(t, r, service.OutlookRequest{Players: []string{"Mkt00"}, Gameweeks: 3})

	var total float64
	for _, w := range v.Players[0].Weeks {
		if w.AsCaptain != math.Round(2*w.XP*10)/10 || w.AsTripleCaptain != math.Round(3*w.XP*10)/10 || w.TripleCaptainExtra != w.XP {
			t.Errorf("GW%d: xp %v, captain %v, triple %v, extra %v: a reader who multiplies the shown xp must get these", w.Gameweek, w.XP, w.AsCaptain, w.AsTripleCaptain, w.TripleCaptainExtra)
		}
		total += w.XP
	}
	if math.Abs(v.Players[0].TotalXP-total) > 0.051 {
		t.Errorf("total %v vs the weeks' sum %v", v.Players[0].TotalXP, total)
	}
}

func TestOutlook_ComparesAPlayerWithTheRestOfTheManagersSquad(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})

	v := outlook(t, r, service.OutlookRequest{Players: []string{"Mkt00", "Own10"}, Gameweeks: 2})

	strong, own := v.Players[0], v.Players[1]
	if !own.Owned || strong.Owned {
		t.Errorf("ownership: strong %v own %v", strong.Owned, own.Owned)
	}
	w := strong.Weeks[0]
	if w.BestOther == "" || w.BestOtherXP <= 0 || math.Abs(w.LeadOverSquad-(w.XP-w.BestOtherXP)) > 0.11 {
		t.Errorf("lead over the squad: %+v", w)
	}
	for _, o := range own.Weeks {
		if o.BestOther == "Own10" {
			t.Errorf("a player is not compared with himself: %+v", o)
		}
	}
	if w.LeadOverSquad <= 0 {
		t.Errorf("a 9-a-week goalkeeper leads this squad: %+v", w)
	}
}

func TestOutlook_WithNoNamesItLooksAtTheCaptainAndTheBestOfTheRest(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})

	v := outlook(t, r, service.OutlookRequest{})

	if len(v.Players) != 5 || v.Gameweeks != 4 {
		t.Fatalf("players %d gameweeks %d", len(v.Players), v.Gameweeks)
	}
	if v.Players[0].Name != "Own10" { // slot 10 is the captain
		t.Errorf("the captain comes first, got %s", v.Players[0].Name)
	}
	seen := map[string]bool{}
	for _, p := range v.Players {
		if seen[p.Name] || !p.Owned {
			t.Errorf("duplicate or not owned: %+v", p)
		}
		seen[p.Name] = true
	}
}

func TestOutlook_NamesAreMatchedLikeEverywhereElseAndErrorsExplain(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		aheadFixtures(w)
		addPlayer(w, 300, fpl.GKP, 6, 40, 1)
		addPlayer(w, 301, fpl.DEF, 7, 45, 3)
	}})
	var in *service.InputError

	if v := outlook(t, r, service.OutlookRequest{Players: []string{"mkt00", "MKT01"}}); len(v.Players) != 2 {
		t.Errorf("case must not matter: %d", len(v.Players))
	}
	if v := outlook(t, r, service.OutlookRequest{Players: []string{"Mkt00", "mkt00"}}); len(v.Players) != 1 {
		t.Errorf("the same player named twice is one: %d", len(v.Players))
	}
	_, err := r.svc.Outlook(context.Background(), service.OutlookRequest{Players: []string{"Extra"}})
	if !errors.As(err, &in) || !strings.Contains(err.Error(), "Extra") || !strings.Contains(err.Error(), "C06") {
		t.Errorf("two players called Extra: err = %v, want a list that says which", err)
	}
	if _, err := r.svc.Outlook(context.Background(), service.OutlookRequest{Players: []string{"Nobody"}}); !errors.As(err, &in) {
		t.Errorf("unknown name: %v", err)
	}
}

func TestOutlook_Limits(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	var in *service.InputError
	for name, req := range map[string]service.OutlookRequest{
		"too many weeks":   {Players: []string{"Mkt00"}, Gameweeks: 9},
		"negative weeks":   {Players: []string{"Mkt00"}, Gameweeks: -1},
		"too many players": {Players: []string{"a", "b", "c", "d", "e", "f", "g"}},
		"an empty name":    {Players: []string{"  "}},
	} {
		if _, err := r.svc.Outlook(context.Background(), req); !errors.As(err, &in) {
			t.Errorf("%s: err = %v, want an InputError", name, err)
		}
	}
}

func TestOutlook_StopsAtTheEndOfTheSeasonAndFlagsAnInjuredPlayer(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		aheadFixtures(w)
		for i := range w.events {
			w.events[i].IsNext = w.events[i].ID == 8
		}
		for i := range w.players {
			if w.players[i].ID == strongGK {
				w.players[i].Status = "i"
			}
		}
	}})

	v := outlook(t, r, service.OutlookRequest{Players: []string{"Mkt00"}, Gameweeks: 8})

	if !hasNote(v.Notes, "is injured") || !hasNote(v.Notes, "not modelled") {
		t.Errorf("notes = %v", v.Notes)
	}
	if v.Players[0].Status != "injured" {
		t.Errorf("status = %q", v.Players[0].Status)
	}
}

func TestOutlook_WorksForAnyoneEvenWithoutATeam(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	svcNoTeam := service.New(service.Deps{Store: r.store, FPL: r.fpl, Now: func() time.Time { return testNow }}, service.Config{})

	v, err := svcNoTeam.Outlook(context.Background(), service.OutlookRequest{Players: []string{"Mkt00"}})
	if err != nil || len(v.Players) != 1 || v.Players[0].Weeks[0].BestOther != "" {
		t.Errorf("err %v view %+v: a look at one player needs no squad, and then there is nothing to compare with", err, v)
	}
	if _, err := svcNoTeam.Outlook(context.Background(), service.OutlookRequest{}); err == nil {
		t.Error("with no names and no squad there is nobody to look at")
	}
}
