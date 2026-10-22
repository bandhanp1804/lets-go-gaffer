package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

func TestParseTeamArgs(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		args      []string
		wantID    int
		wantSwaps []string
		wantErr   string
	}{
		{"id from the environment", "1253022", nil, 1253022, nil, ""},
		{"positional id beats the environment", "1253022", []string{"42"}, 42, nil, ""},
		{"-team flag", "", []string{"-team", "42"}, 42, nil, ""},
		{"swaps are collected", "7", []string{"-swap", "A>B", "-swap", "C>D"}, 7, []string{"A>B", "C>D"}, ""},
		{"flags then a positional id", "", []string{"-swap", "A>B", "42"}, 42, []string{"A>B"}, ""},
		{"no id anywhere", "", nil, 0, nil, "team id"},
		{"not a number", "", []string{"abc"}, 0, nil, "invalid team id"},
		{"zero", "", []string{"0"}, 0, nil, "invalid team id"},
		{"two positionals", "", []string{"1", "2"}, 0, nil, "usage"},
		{"both -team and a positional", "", []string{"-team", "1", "2"}, 0, nil, "usage"},
		{"unknown flag", "7", []string{"-bogus"}, 0, nil, "usage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GAFFER_TEAM_ID", tc.env)

			id, swaps, err := parseTeamArgs("team", "usage: gaffer team", tc.args)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || id != tc.wantID || !slices.Equal(swaps, tc.wantSwaps) {
				t.Errorf("got id %d swaps %v err %v; want %d %v", id, swaps, err, tc.wantID, tc.wantSwaps)
			}
		})
	}
}

func TestApplyPending(t *testing.T) {
	team, _ := legalTeam() // PlayerA..PlayerO, ids 101..115
	market := append([]fpl.Player(nil), team.Squad.Players...)
	market = append(market, fpl.Player{ID: 300, WebName: "Newbie", TeamID: 9, Position: fpl.FWD, NowCost: 60, Status: "a"})
	var b strings.Builder

	t.Run("no swaps changes nothing and prints nothing", func(t *testing.T) {
		b.Reset()

		got, changes, err := applyPending(&b, team, market, nil)

		if err != nil || len(changes) != 0 || got.Squad.Players[0].ID != team.Squad.Players[0].ID || b.Len() != 0 {
			t.Errorf("err=%v changes=%d output=%q", err, len(changes), b.String())
		}
	})
	t.Run("a swap is applied and announced as user-supplied", func(t *testing.T) {
		b.Reset()

		got, changes, err := applyPending(&b, team, market, []string{"PlayerO>Newbie"}) // PlayerO is a forward

		if err != nil || len(changes) != 1 {
			t.Fatalf("err=%v changes=%v", err, changes)
		}
		if got.Squad.Players[14].ID != 300 {
			t.Errorf("the new player is not in the sold player's slot: %v", got.Squad.Players[14])
		}
		out := b.String()
		for _, want := range []string{"PlayerO → Newbie", "pending", "not read from FPL"} {
			if !strings.Contains(out, want) {
				t.Errorf("notice missing %q: %q", want, out)
			}
		}
	})
	t.Run("a bad name is an error that names it", func(t *testing.T) {
		b.Reset()

		_, _, err := applyPending(&b, team, market, []string{"PlayerO>Nobody"})

		if err == nil || !strings.Contains(err.Error(), "Nobody") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a malformed swap is an error", func(t *testing.T) {
		b.Reset()

		if _, _, err := applyPending(&b, team, market, []string{"PlayerO"}); err == nil {
			t.Error("want an error for a swap without an arrow")
		}
	})
}

func TestPrintTransfers_MentionsPendingTransfersAndTheirHit(t *testing.T) {
	v := transferFixture()
	v.Fin.PendingTransfers = 2
	v.Fin.PendingHit = 4
	v.Pending = "Havertz → Calvert-Lewin, Cash → Vuskovic"
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	for _, want := range []string{"Havertz → Calvert-Lewin", "already made 2", "-4"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
