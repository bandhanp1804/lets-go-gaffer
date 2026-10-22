package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

// legalTeam is a 4-4-2 squad worth £105.0m: legal for its owner, over £100.0m.
func legalTeam() (myteam.Team, []fpl.Team) {
	pos := func(p int) fpl.Position {
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
	var s fpl.Squad
	for p := 1; p <= 15; p++ {
		s.Players = append(s.Players, fpl.Player{
			ID: 100 + p, WebName: "Player" + string(rune('A'+p-1)), TeamID: (p-1)%5 + 1, Position: pos(p), NowCost: 70, Status: "a", Form: "5.0",
		})
		if p <= 11 {
			s.Starting = append(s.Starting, 100+p)
		}
	}
	s.Captain, s.ViceCaptain = 110, 106
	teams := []fpl.Team{{ID: 1, ShortName: "ARS"}, {ID: 2, ShortName: "MCI"}, {ID: 3, ShortName: "CHE"}, {ID: 4, ShortName: "LIV"}, {ID: 5, ShortName: "AVL"}}
	return myteam.Team{
		Entry:   fpl.Entry{Name: "Gaffer FC", FirstName: "Pat", LastName: "Doe"},
		Event:   7,
		Squad:   s,
		History: fpl.EntryHistory{Points: 69, OverallRank: 1757258, Bank: 1, Value: 1050},
		Chip:    "wildcard",
	}, teams
}

func TestCommas(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1757258: "1,757,258", -1234: "-1,234"} {
		if got := commas(in); got != want {
			t.Errorf("commas(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintTeam_HeaderAndRulesCheck(t *testing.T) {
	team, teams := legalTeam()
	var b strings.Builder

	printTeam(&b, team, teams)

	out := b.String()
	for _, want := range []string{
		"Gaffer FC", "Pat Doe", "GW7", "69 pts", "1,757,258", "bank £0.1m", "value £105.0m", "chip: wildcard",
		"(C)", "(VC)", "Squad check: OK",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The £100.0m starting-budget rule must not be applied to an existing squad.
	if strings.Contains(out, "budget") {
		t.Errorf("a legitimately valuable squad was reported against the starting budget:\n%s", out)
	}
	if !strings.Contains(out, "No injury or availability flags") {
		t.Errorf("a fully fit squad should say so:\n%s", out)
	}
}

func TestPrintTeam_FlagsPlayersWithNewsOrDoubts(t *testing.T) {
	team, teams := legalTeam()
	chance := 50
	added := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	team.Squad.Players[2].Status = "d"
	team.Squad.Players[2].ChanceOfPlayingNextRound = &chance
	team.Squad.Players[2].News, team.Squad.Players[2].NewsAdded = "Thigh injury - 50% chance of playing", &added
	team.Squad.Players[13].Status = "i"
	team.Squad.Players[13].News = "Knee injury - Unknown return date"
	var b strings.Builder

	printTeam(&b, team, teams)

	out := b.String()
	for _, want := range []string{"PlayerC", "doubtful", "50%", "Thigh injury - 50% chance of playing", "PlayerN", "injured", "Knee injury"} {
		if !strings.Contains(out, want) {
			t.Errorf("flags missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "No injury or availability flags") {
		t.Errorf("claimed no flags despite two:\n%s", out)
	}
	// Only flagged players appear under the heading.
	flags := out[strings.Index(out, "Availability"):]
	if strings.Contains(flags, "PlayerA") {
		t.Errorf("a fit player was listed as a flag:\n%s", flags)
	}
}

func TestPrintTeam_ReportsRuleViolations(t *testing.T) {
	team, teams := legalTeam()
	team.Squad.Players = team.Squad.Players[:14] // a missing player
	var b strings.Builder

	printTeam(&b, team, teams)

	out := b.String()
	if !strings.Contains(out, "Squad check: FAILED") || !strings.Contains(out, "squad_size") {
		t.Errorf("want a failed check naming squad_size:\n%s", out)
	}
}

func TestStatusLabel(t *testing.T) {
	for in, want := range map[string]string{"a": "available", "d": "doubtful", "i": "injured", "s": "suspended", "u": "unavailable", "n": "n"} {
		if got := statusLabel(in); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
