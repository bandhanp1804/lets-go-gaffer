package main

import (
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
)

func entry(id int, name string, pos fpl.Position, team int, xp float64) lineup.Entry {
	return lineup.Entry{Player: fpl.Player{ID: id, WebName: name, Position: pos, TeamID: team, Status: "a"}, XP: xp}
}

// lineupFixture is a 4-4-2 with two bench outfielders close to starting.
func lineupFixture() lineupView {
	starting := []lineup.Entry{
		entry(101, "Raya", fpl.GKP, 1, 4.1),
		entry(102, "Calafiori", fpl.DEF, 1, 5.2), entry(103, "Guéhi", fpl.DEF, 2, 4.4),
		entry(104, "DeCuyper", fpl.DEF, 3, 4.0), entry(105, "Konsa", fpl.DEF, 4, 3.8),
		entry(106, "Palmer", fpl.MID, 5, 6.1), entry(107, "Rogers", fpl.MID, 5, 5.0),
		entry(108, "Neto", fpl.MID, 5, 4.9), entry(109, "Doku", fpl.MID, 2, 4.6),
		entry(110, "Haaland", fpl.FWD, 2, 8.4), entry(111, "JoaoPedro", fpl.FWD, 5, 5.5),
	}
	bench := []lineup.Entry{
		entry(112, "Tzolakis", fpl.GKP, 6, 2.0),
		entry(113, "Cash", fpl.DEF, 4, 3.7),
		entry(114, "Belloumi", fpl.MID, 6, 3.0),
		entry(115, "Havertz", fpl.FWD, 1, 2.5),
	}
	starting[3].Player.Status = "d"
	chance := 50
	starting[3].Player.ChanceOfPlayingNextRound = &chance
	starting[3].Player.News = "Knock - 50% chance of playing"

	team, teams := legalTeam()
	team.Event = 7
	team.Squad.Starting = []int{101, 102, 103, 104, 106, 107, 108, 109, 110, 111, 115} // last week: Havertz started, Konsa did not
	return lineupView{
		Team: team, Teams: append(teams, fpl.Team{ID: 6, ShortName: "HUL"}), Next: 8,
		Advice: lineup.Advice{
			Formation: "4-4-2", Starting: starting, Bench: bench,
			Captain: starting[9], ViceCaptain: starting[5], Total: 46.5,
			CloseCalls: []lineup.CloseCall{{Bench: bench[1], Displaces: starting[4], Margin: 0.1}},
			Formations: []lineup.FormationOption{
				{Formation: "4-4-2", Total: 46.5, Delta: 0},
				{Formation: "3-5-2", Total: 46.1, Delta: -0.4},
			},
		},
		Opponents: map[int][]fpl.Opponent{
			101: {{Team: 2, Home: true, Difficulty: 2}},
			102: {{Team: 3, Home: false, Difficulty: 4}},
			110: {{Team: 1, Home: true, Difficulty: 3}, {Team: 4, Home: false, Difficulty: 2}}, // a double gameweek
			// 103 has no fixture at all: a blank
		},
	}
}

func TestPrintLineup_ShowsTheXIBenchAndWhy(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	for _, want := range []string{
		"Gaffer FC", "best XI for GW8", "4-4-2", "46.5",
		"Raya", "Haaland", "(C)", "(VC)",
		"Bench", "Tzolakis", "Cash", "Belloumi", "Havertz",
		"xP  8.4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The spare goalkeeper is the first sub, as in FPL.
	if strings.Index(out, "Tzolakis") > strings.Index(out, "Cash") {
		t.Errorf("the spare goalkeeper should lead the bench:\n%s", out)
	}
}

func TestPrintLineup_ShowsEachPlayersOpponent(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	for _, want := range []string{"MCI (H)", "CHE (A)", "no fixture"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// A double gameweek lists both matches.
	if !strings.Contains(out, "ARS (H)") || !strings.Contains(out, "LIV (A)") || !strings.Contains(out, " + ") {
		t.Errorf("a double gameweek should show both opponents:\n%s", out)
	}
}

func TestPrintLineup_ANearMissIsNamedWithItsMargin(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	if !strings.Contains(out, "Close calls") || !strings.Contains(out, "Cash") || !strings.Contains(out, "Konsa") || !strings.Contains(out, "0.1") {
		t.Errorf("want Cash named as a near miss for Konsa's place, 0.1 xP behind:\n%s", out)
	}
}

func TestPrintLineup_NoCloseCallsSaysSo(t *testing.T) {
	v := lineupFixture()
	v.Advice.CloseCalls = nil
	var b strings.Builder

	printLineup(&b, v)

	if !strings.Contains(b.String(), "No close calls") {
		t.Errorf("want an explicit no-close-calls line:\n%s", b.String())
	}
}

func TestPrintLineup_CaptainMarginAndFormations(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	// Haaland 8.4 leads Palmer 6.1 by 2.3.
	if !strings.Contains(out, "Haaland") || !strings.Contains(out, "2.3") {
		t.Errorf("want the captain's lead over the vice (2.3 xP):\n%s", out)
	}
	if !strings.Contains(out, "3-5-2") || !strings.Contains(out, "-0.4") {
		t.Errorf("want the next-best formation and what it would cost:\n%s", out)
	}
}

func TestPrintLineup_ChangesFromLastLineup(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	if !strings.Contains(out, "Changes from your GW7 lineup") {
		t.Fatalf("want a comparison with last gameweek's XI:\n%s", out)
	}
	// Konsa (105) is in now; Havertz (115) is out. Names come from the advised squad.
	if !strings.Contains(out, "IN") || !strings.Contains(out, "OUT") || !strings.Contains(out, "Konsa") || !strings.Contains(out, "Havertz") {
		t.Errorf("want Konsa in and Havertz out:\n%s", out)
	}
}

func TestPrintLineup_NoChangesIsStated(t *testing.T) {
	v := lineupFixture()
	v.Team.Squad.Starting = nil
	for _, e := range v.Advice.Starting {
		v.Team.Squad.Starting = append(v.Team.Squad.Starting, e.Player.ID)
	}
	var b strings.Builder

	printLineup(&b, v)

	if !strings.Contains(b.String(), "same XI as your GW7 lineup") {
		t.Errorf("want a same-lineup line:\n%s", b.String())
	}
}

func TestPrintLineup_FlagsADoubtfulStarter(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture())

	out := b.String()
	if !strings.Contains(out, "doubtful") || !strings.Contains(out, "50%") {
		t.Errorf("a doubtful starter must be flagged where the manager will see it:\n%s", out)
	}
}

func TestFormatOpponents(t *testing.T) {
	short := map[int]string{1: "ARS", 2: "MCI"}
	tests := []struct {
		name string
		in   []fpl.Opponent
		want string
	}{
		{"home", []fpl.Opponent{{Team: 2, Home: true, Difficulty: 2}}, "MCI (H) d2"},
		{"away", []fpl.Opponent{{Team: 1, Home: false, Difficulty: 4}}, "ARS (A) d4"},
		{"double", []fpl.Opponent{{Team: 1, Home: true, Difficulty: 2}, {Team: 2, Home: false, Difficulty: 5}}, "ARS (H) d2 + MCI (A) d5"},
		{"blank", nil, "no fixture"},
		{"unknown team", []fpl.Opponent{{Team: 99, Home: true, Difficulty: 3}}, "? (H) d3"},
	}
	for _, tc := range tests {
		if got := formatOpponents(tc.in, short); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPrintLineup_ANearTieForCaptainIsCalledATossUp(t *testing.T) {
	v := lineupFixture()
	v.Advice.ViceCaptain.XP = v.Advice.Captain.XP - 0.04 // practically level
	var b strings.Builder

	printLineup(&b, v)

	out := b.String()
	if !strings.Contains(out, "toss-up") || !strings.Contains(out, "0.04") {
		t.Errorf("a captain choice this close should be flagged, with the real gap shown:\n%s", out)
	}
	if strings.Contains(out, "leads by 0.0 xP") {
		t.Errorf("a gap of 0.04 must not be printed as a confident lead of 0.0:\n%s", out)
	}
}

func TestPrintLineup_AClearCaptainIsStatedWithItsLead(t *testing.T) {
	var b strings.Builder

	printLineup(&b, lineupFixture()) // Haaland 8.4 vs Palmer 6.1

	if out := b.String(); !strings.Contains(out, "leads by 2.3 xP") || strings.Contains(out, "toss-up for the captain") {
		t.Errorf("a clear captain should read as one:\n%s", out)
	}
}

func TestPrintLineup_SaysWhenTheComparisonIncludesPendingSwaps(t *testing.T) {
	v := lineupFixture()
	var plain, swapped strings.Builder

	printLineup(&plain, v)
	v.Swapped = true
	printLineup(&swapped, v)

	if strings.Contains(plain.String(), "pending swaps") {
		t.Errorf("no swaps were entered, so the label must not mention them:\n%s", plain.String())
	}
	if !strings.Contains(swapped.String(), "with your pending swaps slotted in") {
		t.Errorf("the comparison is against a lineup with swaps in it, and must say so:\n%s", swapped.String())
	}
}
