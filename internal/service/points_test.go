package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// gw8InPlay makes GW8 the gameweek being played (its deadline has passed) and GW9 the next.
func gw8InPlay(w *world) {
	for i := range w.events {
		switch w.events[i].ID {
		case 8:
			w.events[i].IsNext, w.events[i].IsCurrent = false, true
		case 9:
			w.events[i].IsNext = true
		}
	}
}

func myTeam(t *testing.T, r *rig) service.MyTeamView {
	t.Helper()
	v, err := r.svc.MyTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// In the fake squad, slot p holds player 100+p; slot 10 (id 110) is the captain, 6 (106) the vice;
// slots 1-11 start, 12-15 are the bench.

func TestPoints_ACaptainScoresTwiceAndTheBenchNotAtAll(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.livePoints = map[int]int{101: 2, 105: 6, 110: 9, 112: 8, 115: 3} // 110 captain, 112 and 115 on the bench
	r.fpl.liveMinutes = map[int]int{101: 90, 105: 90, 110: 90, 112: 90, 115: 90}
	r.fpl.transferCost = 4

	v := myTeam(t, r)

	p := v.Points
	if p == nil {
		t.Fatal("no points")
	}
	if p.Gameweek != 7 || p.InPlay || !p.Started {
		t.Errorf("gameweek %d in play %v started %v: with GW7 the last finished, those are its final points", p.Gameweek, p.InPlay, p.Started)
	}
	if want := 2 + 6 + 9*2; p.Gross != want {
		t.Errorf("gross = %d, want %d (the captain counted twice, the bench not at all)", p.Gross, want)
	}
	if p.Hit != 4 || p.Net != p.Gross-4 || p.OnBench != 8+3 {
		t.Errorf("hit %d net %d bench %d", p.Hit, p.Net, p.OnBench)
	}
	if p.SeasonPoints != 400 || p.OverallRank != 1234567 {
		t.Errorf("season %d rank %d: FPL's own figures should be passed on", p.SeasonPoints, p.OverallRank)
	}
	if len(p.Players) != 15 || p.Players[0].ID != 101 || p.Players[14].ID != 115 || !p.Players[14].Bench || p.Players[9].Counted != 18 || !p.Players[9].Captain {
		t.Errorf("players = %+v", p.Players)
	}
	if !strings.Contains(p.Note, "finished") {
		t.Errorf("note = %q", p.Note)
	}
}

func TestPoints_EachPlayerCardGetsItsPointsForTheGameweek(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.livePoints = map[int]int{101: 2, 110: 9}

	v := myTeam(t, r)

	byName := map[string]*int{}
	for _, pl := range append(append([]service.PlayerView{}, v.Starting...), v.Bench...) {
		byName[pl.Name] = pl.GWPoints
	}
	if byName["Own01"] == nil || *byName["Own01"] != 2 || byName["Own10"] == nil || *byName["Own10"] != 9 {
		t.Errorf("points on cards: %v", byName)
	}
	if byName["Own02"] == nil || *byName["Own02"] != 0 {
		t.Errorf("a player who scored nothing shows 0, not nothing: %v", byName["Own02"])
	}
}

func TestPoints_AGameweekInPlayIsProvisionalAndSaysWhetherAnyoneHasPlayed(t *testing.T) {
	r := newRig(t, rigOpts{mutate: gw8InPlay})

	before := myTeam(t, r).Points
	if before == nil || before.Gameweek != 8 || !before.InPlay || before.Started || !strings.Contains(before.Note, "none of your players") {
		t.Fatalf("before kick-off: %+v", before)
	}
	if before.Gross != 0 || before.Net != 0 {
		t.Errorf("everyone is on zero: %+v", before)
	}

	r2 := newRig(t, rigOpts{mutate: gw8InPlay})
	r2.fpl.livePoints = map[int]int{103: 5}
	r2.fpl.liveMinutes = map[int]int{103: 45}
	during := myTeam(t, r2).Points
	if !during.InPlay || !during.Started || during.Gross != 5 || !strings.Contains(during.Note, "provisional") {
		t.Errorf("during: %+v", during)
	}
}

// Other managers' players may be playing already. What matters is whether this team's have.
func TestPoints_WaitingMeansWaitingForThisTeamsPlayersNotForAnyMatch(t *testing.T) {
	r := newRig(t, rigOpts{mutate: gw8InPlay})
	r.fpl.livePoints = map[int]int{210: 7} // a player this team does not own has already scored
	r.fpl.liveMinutes = map[int]int{210: 90}

	p := myTeam(t, r).Points

	if p.Started || p.Gross != 0 {
		t.Errorf("started %v gross %d: someone else's match is not this team's", p.Started, p.Gross)
	}
}

func TestPoints_BeforeKickOffTheOnlyScoreIsTheTransferHit(t *testing.T) {
	r := newRig(t, rigOpts{mutate: gw8InPlay})
	r.fpl.transferCost = 4

	p := myTeam(t, r).Points

	if p.Gross != 0 || p.Hit != 4 || p.Net != -4 {
		t.Errorf("gross %d hit %d net %d", p.Gross, p.Hit, p.Net)
	}
}

func TestPoints_AutomaticSubstitutionsAreApplied(t *testing.T) {
	r := newRig(t, rigOpts{})
	// Starting defender 102 did not play; bench defender 113 came on for him.
	r.fpl.autoSubs = []fpl.AutoSub{{In: 113, Out: 102, Event: 7}}
	r.fpl.livePoints = map[int]int{102: 0, 113: 6, 101: 2}
	r.fpl.liveMinutes = map[int]int{113: 90, 101: 90}

	p := myTeam(t, r).Points

	if p.Gross != 2+6 {
		t.Errorf("gross = %d, want the substitute's 6 counted once", p.Gross)
	}
	var on, off *service.PointsPlayerView
	for i := range p.Players {
		switch p.Players[i].ID {
		case 113:
			on = &p.Players[i]
		case 102:
			off = &p.Players[i]
		}
	}
	if !on.SubbedOn || on.Bench || on.Multiplier != 1 || !off.SubbedOff || off.Multiplier != 0 {
		t.Errorf("on %+v off %+v", on, off)
	}
}

func TestPoints_TheViceCaptainTakesTheArmbandOnlyOnceTheGameweekIsOver(t *testing.T) {
	setup := func(mutate func(*world)) *service.PointsView {
		r := newRig(t, rigOpts{mutate: mutate})
		r.fpl.livePoints = map[int]int{106: 7, 110: 0}
		r.fpl.liveMinutes = map[int]int{106: 90} // the captain (110) never played; the vice (106) did
		return myTeam(t, r).Points
	}

	finished := setup(nil)
	if finished.Gross != 14 {
		t.Errorf("finished: the vice-captain's 7 counts twice, got %d", finished.Gross)
	}
	inPlay := setup(gw8InPlay)
	if inPlay.Gross != 7 {
		t.Errorf("in play: the captain may simply not have played yet, so nothing moves; got %d", inPlay.Gross)
	}
}

// If the squad itself cannot be read there is nothing to show; but if only the points feed fails,
// the squad is still worth having, so the page gets it with a note.
func TestPoints_AFailingPointsFeedLeavesTheSquadAndSaysWhy(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.liveErr = context.DeadlineExceeded

	v, err := r.svc.MyTeam(context.Background())

	if err != nil {
		t.Fatalf("a failing points feed must not take the squad with it: %v", err)
	}
	if v.Points != nil || !hasNote(v.Notes, "points could not be read") || len(v.Starting) != 11 {
		t.Errorf("points %+v notes %v starting %d", v.Points, v.Notes, len(v.Starting))
	}
}

func TestPoints_AreKeptBrieflyAndRefreshedMoreOftenWhileTheGameweekIsInPlay(t *testing.T) {
	inPlay := newRig(t, rigOpts{mutate: gw8InPlay})
	myTeam(t, inPlay)
	myTeam(t, inPlay)
	if inPlay.fpl.liveCalls != 1 {
		t.Errorf("two requests at once should share one fetch, got %d", inPlay.fpl.liveCalls)
	}
	*inPlay.now = inPlay.now.Add(2 * time.Minute) // past a minute, inside the 3-minute squad cache
	myTeam(t, inPlay)
	if inPlay.fpl.liveCalls != 2 {
		t.Errorf("points move during a gameweek, so after a minute they are read again: %d fetches", inPlay.fpl.liveCalls)
	}

	done := newRig(t, rigOpts{})
	myTeam(t, done)
	*done.now = done.now.Add(2 * time.Minute)
	myTeam(t, done)
	if done.fpl.liveCalls != 1 {
		t.Errorf("a finished gameweek does not move, so it is kept longer: %d fetches", done.fpl.liveCalls)
	}
}

// The squad shown can be next week's (live, with transfers made since); the points are still those
// of the team that is playing now, and they need no token.
func TestPoints_ComeFromPublicPicksEvenWhenTheSquadIsLive(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(time.Hour))})
	r.fpl.livePoints = map[int]int{101: 3}

	v := myTeam(t, r)

	if v.Source != "live" {
		t.Fatalf("source = %q", v.Source)
	}
	if v.Points == nil || v.Points.Gross != 3 || r.fpl.picksCalls == 0 {
		t.Errorf("points %+v, picks fetched %d times", v.Points, r.fpl.picksCalls)
	}
}

func TestPoints_ForAChosenTeamAreThatTeamsPoints(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam})

	v, err := r.svc.MyTeam(ctx)

	if err != nil || v.Points == nil {
		t.Errorf("err %v points %+v", err, v.Points)
	}
}

// Own03 (id 103) plays for Club3, who host Club4 in GW8 (fixture 3 in the test season).
func matchState(home, away int, started, finished bool, minutes int) func(*world) {
	return func(w *world) {
		gw8InPlay(w)
		for i := range w.fixtures {
			if w.fixtures[i].ID == 3 {
				w.fixtures[i].HomeScore, w.fixtures[i].AwayScore = &home, &away
				w.fixtures[i].Started, w.fixtures[i].Finished, w.fixtures[i].Minutes = started, finished, minutes
				w.fixtures[i].KickoffTime = "2026-10-24T14:00:00Z"
			}
		}
	}
}

func playerPoints(p *service.PointsView, id int) service.PointsPlayerView {
	for _, pl := range p.Players {
		if pl.ID == id {
			return pl
		}
	}
	return service.PointsPlayerView{}
}

func TestPoints_EachPlayerHasHisMatchesWithTheScoreAndWhereThePointsCameFrom(t *testing.T) {
	r := newRig(t, rigOpts{mutate: matchState(2, 1, true, true, 90)})
	r.fpl.livePoints = map[int]int{103: 6}
	r.fpl.liveMinutes = map[int]int{103: 90}
	r.fpl.liveExplain = map[int][]fpl.LiveMatch{103: {{Fixture: 3, Stats: []fpl.LiveStat{
		{Identifier: "minutes", Points: 2, Value: 90},
		{Identifier: "clean_sheets", Points: 4, Value: 1},
		{Identifier: "yellow_cards", Points: 0, Value: 0}, // nothing happened: not a row
	}}}}

	pl := playerPoints(myTeam(t, r).Points, 103)

	if len(pl.Matches) != 1 {
		t.Fatalf("matches = %+v", pl.Matches)
	}
	m := pl.Matches[0]
	if m.Home != "Club3" || m.Away != "Club4" || m.HomeScore == nil || *m.HomeScore != 2 || *m.AwayScore != 1 || m.Status != "FT" || m.Kickoff != "2026-10-24T14:00:00Z" {
		t.Errorf("match = %+v", m)
	}
	if len(m.Stats) != 2 || m.Stats[0] != (service.StatRowView{Label: "Minutes played", Value: 90, Points: 2}) || m.Stats[1] != (service.StatRowView{Label: "Clean sheets", Value: 1, Points: 4}) {
		t.Errorf("stats = %+v", m.Stats)
	}
	if m.Points != 6 || m.Points != pl.Points {
		t.Errorf("match points %d, player points %d: the rows must add up to the total", m.Points, pl.Points)
	}
}

func TestPoints_AMatchInProgressShowsItsMinuteAndOneNotStartedShowsNoStatistics(t *testing.T) {
	live := newRig(t, rigOpts{mutate: matchState(1, 0, true, false, 67)})
	if m := playerPoints(myTeam(t, live).Points, 103).Matches[0]; m.Status != "67'" || *m.HomeScore != 1 || *m.AwayScore != 0 {
		t.Errorf("in progress: %+v", m)
	}

	waiting := newRig(t, rigOpts{mutate: gw8InPlay})
	m := playerPoints(myTeam(t, waiting).Points, 103).Matches[0]
	if m.Status != "Not started" || m.HomeScore != nil || m.AwayScore != nil || len(m.Stats) != 0 || m.Points != 0 {
		t.Errorf("not started: %+v", m)
	}
}

func TestPoints_ADoubleGameweekListsBothMatches(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		gw8InPlay(w)
		w.fixtures = append(w.fixtures, fpl.Fixture{ID: 99, Event: 8, HomeTeam: 5, AwayTeam: 3, HomeDifficulty: 3, AwayDifficulty: 3}) // Club3 play again
	}})
	r.fpl.liveExplain = map[int][]fpl.LiveMatch{103: {{Fixture: 99, Stats: []fpl.LiveStat{{Identifier: "minutes", Points: 1, Value: 30}}}}}

	pl := playerPoints(myTeam(t, r).Points, 103)

	if len(pl.Matches) != 2 {
		t.Fatalf("matches = %+v", pl.Matches)
	}
	if pl.Matches[0].Points != 0 || pl.Matches[1].Points != 1 || pl.Matches[1].Home != "Club5" {
		t.Errorf("matches = %+v", pl.Matches)
	}
}

func TestPoints_ABenchPlayerAndAStatNobodyNamedStillGetBreakdowns(t *testing.T) {
	r := newRig(t, rigOpts{mutate: matchState(0, 0, true, true, 90)})
	r.fpl.liveExplain = map[int][]fpl.LiveMatch{103: {{Fixture: 3, Stats: []fpl.LiveStat{{Identifier: "defensive_contribution", Points: 2, Value: 12}, {Identifier: "brand_new_stat", Points: 1, Value: 3}}}}}

	p := myTeam(t, r).Points

	m := playerPoints(p, 103).Matches[0]
	if len(m.Stats) != 2 || m.Stats[0].Label != "Defensive contribution" || m.Stats[1].Label != "Brand new stat" {
		t.Errorf("stats = %+v: a statistic FPL adds later must still be shown, with a readable name", m.Stats)
	}
	if got := playerPoints(p, 115); len(got.Matches) == 0 {
		t.Errorf("a bench player has matches too: %+v", got)
	}
}
