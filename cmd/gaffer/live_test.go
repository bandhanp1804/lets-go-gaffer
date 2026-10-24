package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

var now = time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)

// token builds an unsigned bearer token expiring at exp, with a recognisable secret inside.
func token(exp time.Time) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return "Bearer " + enc(`{"alg":"none"}`) + "." + enc(fmt.Sprintf(`{"exp":%d,"sub":"SECRET-SUBJECT"}`, exp.Unix())) + ".SECRET-SIGNATURE"
}

type fakeLive struct {
	team  *fpl.MyTeam
	err   error
	calls int
}

func (f *fakeLive) FetchMyTeam(context.Context, int) (*fpl.MyTeam, error) {
	f.calls++
	return f.team, f.err
}

func TestTryLive(t *testing.T) {
	valid := token(now.Add(47 * time.Minute))
	tests := []struct {
		name      string
		session   string
		src       *fakeLive
		wantLive  bool
		wantCalls int
		wantOut   []string
		denyOut   []string
	}{
		{"no session configured is silent and makes no request", "", &fakeLive{team: &fpl.MyTeam{}}, false, 0, nil, []string{"session", "FPL"}},
		{"a valid session reads the live squad and says how long it lasts", valid, &fakeLive{team: &fpl.MyTeam{}}, true, 1,
			[]string{"live squad", "47 min"}, nil},
		{"an expired token is not even sent", token(now.Add(-3 * time.Hour)), &fakeLive{team: &fpl.MyTeam{}}, false, 0,
			[]string{"expired", "3h", "FPL_AUTH_HEADER"}, nil},
		{"a token about to expire is used but flagged", token(now.Add(4 * time.Minute)), &fakeLive{team: &fpl.MyTeam{}}, true, 1,
			[]string{"live squad", "4 min", "refresh"}, nil},
		{"FPL rejecting the session falls back with the reason", valid, &fakeLive{err: fmt.Errorf("x: %w", fpl.ErrSessionRejected)}, false, 1,
			[]string{"rejected", "public data", "FPL_AUTH_HEADER"}, nil},
		{"FPL being down is not blamed on the session", valid, &fakeLive{err: errors.New("connection refused")}, false, 1,
			[]string{"Could not read", "public data"}, []string{"rejected"}},
		{"a value that is not a token is simply tried", "opaque-value", &fakeLive{team: &fpl.MyTeam{}}, true, 1, []string{"live squad"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder

			got := tryLive(context.Background(), &b, tc.src, tc.session, 4321, now)

			if (got != nil) != tc.wantLive || tc.src.calls != tc.wantCalls {
				t.Errorf("live=%v calls=%d; want live=%v calls=%d", got != nil, tc.src.calls, tc.wantLive, tc.wantCalls)
			}
			out := b.String()
			for _, want := range tc.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q: %q", want, out)
				}
			}
			for _, deny := range tc.denyOut {
				if strings.Contains(out, deny) {
					t.Errorf("output must not contain %q: %q", deny, out)
				}
			}
			// The credential is never printed, whatever happened.
			for _, leak := range []string{"SECRET", tc.session, "eyJ"} {
				if leak != "" && strings.Contains(out, leak) {
					t.Errorf("output leaks the credential (%q): %q", leak, out)
				}
			}
		})
	}
}

func liveTeamFor(t *testing.T) (myteam.Team, *fpl.MyTeam, []fpl.Player) {
	t.Helper()
	team, _ := legalTeam() // PlayerA..PlayerO, ids 101..115, an XI of the first 11
	live := &fpl.MyTeam{Transfers: fpl.MyTransfers{Cost: 4, Limit: 1, Made: 2, Bank: 9, Value: 1003}}
	for i, p := range team.Squad.Players {
		live.Picks = append(live.Picks, fpl.MyPick{
			Element: p.ID, Position: i + 1, ElementType: p.Position, SellingPrice: 60, PurchasePrice: 58,
			IsCaptain: p.ID == team.Squad.Captain, IsViceCaptain: p.ID == team.Squad.ViceCaptain,
		})
	}
	return team, live, team.Squad.Players
}

func TestCurrentSquad_PrefersTheLiveSquadAndSaysSwapsAreIgnored(t *testing.T) {
	public, live, players := liveTeamFor(t)
	src := &fakeLive{team: live}
	var b strings.Builder

	team, fin, changes, err := currentSquad(context.Background(), &b, src, token(now.Add(time.Hour)), 4321, public, players, []string{"PlayerO>PlayerA"}, now)

	if err != nil {
		t.Fatal(err)
	}
	if fin == nil || fin.Bank != 9 || fin.PendingTransfers != 2 {
		t.Errorf("finances = %+v, want FPL's own (bank 9, 2 made)", fin)
	}
	if len(changes) != 0 || len(team.Squad.Players) != 15 {
		t.Errorf("changes=%d players=%d; the -swap must not be applied on top of live data", len(changes), len(team.Squad.Players))
	}
	out := b.String()
	if !strings.Contains(out, "-swap is ignored") {
		t.Errorf("the user must be told the swap was not used: %q", out)
	}
	if strings.Contains(out, "GW7 deadline") {
		t.Errorf("a live squad is not stale, so no stale warning: %q", out)
	}
}

func TestCurrentSquad_FallsBackToThePublicSquadWithItsWarning(t *testing.T) {
	public, _, players := liveTeamFor(t)
	src := &fakeLive{err: fmt.Errorf("x: %w", fpl.ErrSessionRejected)}
	var b strings.Builder

	team, fin, _, err := currentSquad(context.Background(), &b, src, token(now.Add(time.Hour)), 4321, public, players, nil, now)

	if err != nil {
		t.Fatal(err)
	}
	if fin != nil || team.Squad.Players[0].ID != public.Squad.Players[0].ID {
		t.Errorf("fin=%v; want the public squad and no live finances", fin)
	}
	out := b.String()
	if !strings.Contains(out, "rejected") || !strings.Contains(out, "GW7 deadline") {
		t.Errorf("want the rejection reason and the stale-squad warning: %q", out)
	}
}

func TestCurrentSquad_AppliesSwapsWhenThereIsNoSession(t *testing.T) {
	public, _, players := liveTeamFor(t)
	market := append(append([]fpl.Player(nil), players...), fpl.Player{ID: 300, WebName: "Newbie", TeamID: 9, Position: fpl.FWD, NowCost: 60, Status: "a"})
	var b strings.Builder

	team, fin, changes, err := currentSquad(context.Background(), &b, &fakeLive{}, "", 4321, public, market, []string{"PlayerO>Newbie"}, now)

	if err != nil || fin != nil || len(changes) != 1 || team.Squad.Players[14].ID != 300 {
		t.Errorf("err=%v fin=%v changes=%d", err, fin, len(changes))
	}
}

func TestLiveSummary(t *testing.T) {
	fin := myteam.Finances{
		Bank: 9, PendingTransfers: 2, PendingHit: 4, Free: myteam.FreeTransferState{Next: 0},
		AvailableChips: []string{"wildcard", "freehit"},
	}

	got := liveSummary(fin)

	for _, want := range []string{"bank £0.9m", "2 transfers made", "-4", "wildcard, freehit"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %q", want, got)
		}
	}
	quiet := liveSummary(myteam.Finances{Bank: 20, Free: myteam.FreeTransferState{Next: 2}})
	if !strings.Contains(quiet, "no transfers made") || !strings.Contains(quiet, "2 free") || strings.Contains(quiet, "chips") {
		t.Errorf("quiet summary = %q", quiet)
	}
}

func TestPrintTransfers_LiveDataNamesFPLAsTheSourceAndWarnsWhenNoWildcardIsLeft(t *testing.T) {
	v := transferFixture()
	v.Wildcard = true
	v.Fin.PendingTransfers = 2
	v.Fin.PendingHit = 4
	v.Fin.AvailableChips = []string{"bboost"} // neither wildcard nor free hit is left
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	if !strings.Contains(out, "already made 2") || !strings.Contains(out, "-4") {
		t.Errorf("want the transfers FPL says were made, even without names:\n%s", out)
	}
	if !strings.Contains(out, "no Wildcard or Free Hit") {
		t.Errorf("a wildcard plan with none available must say so:\n%s", out)
	}
}

type fakeBoot struct {
	gws []fpl.Gameweek
	err error
}

func (f fakeBoot) FetchBootstrap(context.Context) (*fpl.Bootstrap, error) {
	return &fpl.Bootstrap{Gameweeks: f.gws}, f.err
}

func TestPlanGameweek(t *testing.T) {
	history := []fpl.PlayerHistory{{GameweekID: 6}, {GameweekID: 7}}
	deadlinePassed := []fpl.Gameweek{{ID: 7, Finished: true}, {ID: 8, IsCurrent: true}, {ID: 9, IsNext: true}}

	t.Run("the gameweek decided for is FPL's next, not the end of the stored data", func(t *testing.T) {
		var b strings.Builder

		upcoming, asOf, err := planGameweek(context.Background(), &b, fakeBoot{gws: deadlinePassed}, history)

		if err != nil || upcoming != 9 || asOf != 8 {
			t.Errorf("upcoming %d asOf %d err %v; want 9, 8, nil: GW8 is locked and has no data yet", upcoming, asOf, err)
		}
		if b.Len() != 0 {
			t.Errorf("nothing is behind yet, so nothing should be printed: %q", b.String())
		}
	})
	t.Run("history behind a finished gameweek is announced", func(t *testing.T) {
		var b strings.Builder
		finished := []fpl.Gameweek{{ID: 7, Finished: true}, {ID: 8, Finished: true}, {ID: 9, IsNext: true}}

		if _, _, err := planGameweek(context.Background(), &b, fakeBoot{gws: finished}, history); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(b.String(), "gaffer sync") || !strings.Contains(b.String(), "GW8") {
			t.Errorf("want the stale-history note: %q", b.String())
		}
	})
	t.Run("no upcoming gameweek is an error", func(t *testing.T) {
		over := []fpl.Gameweek{{ID: 37, Finished: true}, {ID: 38, Finished: true}}

		if _, _, err := planGameweek(context.Background(), &strings.Builder{}, fakeBoot{gws: over}, history); err == nil {
			t.Error("want an error when the season is over")
		}
	})
	t.Run("a failure fetching the schedule surfaces", func(t *testing.T) {
		if _, _, err := planGameweek(context.Background(), &strings.Builder{}, fakeBoot{err: errors.New("fpl down")}, history); err == nil || !strings.Contains(err.Error(), "fpl down") {
			t.Errorf("err = %v", err)
		}
	})
}
