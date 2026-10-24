package service_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

const (
	ownGK       = 101 // the squad's goalkeeper who scores 1 a week
	ownBenchGK  = 112
	ownDEF      = 102
	strongGK    = 201 // Mkt00, scores 9 a week
	weakGK      = 300 // added by addWeakGK: a goalkeeper scoring 1 a week
	clubOneDEF  = 301 // added by addClubOneDEF: a defender at a club the squad already has three of
	expensiveDF = 208 // Mkt07: costs £10.1m
)

func move(out, in int) service.WhatIfMoveRequest { return service.WhatIfMoveRequest{Out: out, In: in} }

func whatIf(t *testing.T, r *rig, req service.WhatIfRequest) service.WhatIfView {
	t.Helper()
	v, err := r.svc.WhatIf(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// addPlayer puts an extra player in the market, scoring pts every gameweek.
func addPlayer(w *world, id int, pos fpl.Position, team, cost, pts int) {
	w.players = append(w.players, fpl.Player{ID: id, WebName: "Extra", TeamID: team, Position: pos, NowCost: cost, Status: "a", Form: "3.0", TotalPoints: 20})
	for gw := 1; gw <= 7; gw++ {
		w.history = append(w.history, fpl.PlayerHistory{PlayerID: id, GameweekID: gw, Fixture: id*10 + gw, TotalPoints: pts, Minutes: 90})
	}
}

func addWeakGK(w *world)     { addPlayer(w, weakGK, fpl.GKP, 6, 40, 1) }
func addClubOneDEF(w *world) { addPlayer(w, clubOneDEF, fpl.DEF, 1, 45, 3) }

func hasInsight(v service.WhatIfView, sub string) bool { return hasNote(v.Insights, sub) }

func TestWhatIf_TheObviousUpgradeIsWorthDoing(t *testing.T) {
	r := newRig(t, rigOpts{})

	v := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}})

	if !v.Valid || len(v.Problems) != 0 {
		t.Fatalf("valid %v problems %v", v.Valid, v.Problems)
	}
	if v.Verdict != "worth_it" || v.NetPoints <= 0 || v.GainPerGameweek <= 0 || v.Hit != 0 || v.Transfers != 1 {
		t.Errorf("verdict %q net %v gain %v hit %d transfers %d", v.Verdict, v.NetPoints, v.GainPerGameweek, v.Hit, v.Transfers)
	}
	if len(v.Weeks) != 4 {
		t.Fatalf("weeks = %d, want the 4 of the horizon", len(v.Weeks))
	}
	for _, w := range v.Weeks {
		if w.Delta < 0 || w.After < w.Before {
			t.Errorf("a better goalkeeper must not lose points in GW%d: %+v", w.Gameweek, w)
		}
	}
	if v.Weeks[0].Gameweek != 8 {
		t.Errorf("the horizon starts at the upcoming gameweek, got GW%d", v.Weeks[0].Gameweek)
	}
	var started bool
	for _, p := range v.After.Starting {
		started = started || p.Name == "Mkt00"
	}
	if !started {
		t.Errorf("the new goalkeeper should start: %v", names(v.After.Starting))
	}
	if !hasInsight(v, "Worth doing") || !hasInsight(v, "Your bank goes from £2.0m to") || !hasInsight(v, "Mkt00 would start in the XI the model would pick") {
		t.Errorf("insights = %q", v.Insights)
	}
	if len(v.Moves) != 1 || v.Moves[0].Out.SellPrice == "" || len(v.Moves[0].InRun) != 4 || !strings.HasPrefix(v.Moves[0].InRun[0], "GW8 ") {
		t.Errorf("move detail = %+v", v.Moves)
	}
}

// The what-if must agree with the Transfers page: replay its top suggestion and every
// figure matches, or the two screens would contradict each other.
func TestWhatIf_ReplayingATransferSuggestionGivesTheSameNumbers(t *testing.T) {
	r := newRig(t, rigOpts{})
	tr, err := r.svc.Transfers(context.Background(), service.TransfersRequest{})
	if err != nil || len(tr.Suggestions) == 0 {
		t.Fatalf("err %v suggestions %d", err, len(tr.Suggestions))
	}
	for i, sg := range tr.Suggestions {
		var moves []service.WhatIfMoveRequest
		for _, m := range sg.Moves {
			moves = append(moves, move(m.Out.ID, m.In.ID))
		}
		free := tr.FreeTransfers

		v := whatIf(t, r, service.WhatIfRequest{Moves: moves, Horizon: tr.Horizon, Free: &free})

		if !v.Valid {
			t.Fatalf("suggestion %d is not valid as a what-if: %v", i, v.Problems)
		}
		if v.NetPoints != sg.NetPoints || v.GainPerGameweek != sg.GainPerGameweek || v.Hit != sg.Hit || v.BankAfter != sg.BankAfter {
			t.Errorf("suggestion %d: transfers page says net %v gain %v hit %d bank %s; what-if says net %v gain %v hit %d bank %s",
				i, sg.NetPoints, sg.GainPerGameweek, sg.Hit, sg.BankAfter, v.NetPoints, v.GainPerGameweek, v.Hit, v.BankAfter)
		}
	}
}

func TestWhatIf_RulesAreChecked(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*world)
		moves   []service.WhatIfMoveRequest
		problem string
	}{
		{"sell someone you do not own", nil, []service.WhatIfMoveRequest{move(205, 206)}, "not in your squad"},
		{"buy a player who does not exist", nil, []service.WhatIfMoveRequest{move(ownGK, 99999)}, "does not exist"},
		{"buy someone you already own", nil, []service.WhatIfMoveRequest{move(ownGK, ownBenchGK)}, "already in your squad"},
		{"like for like", nil, []service.WhatIfMoveRequest{move(ownGK, 202)}, "like for like"},
		{"sell the same player twice", nil, []service.WhatIfMoveRequest{move(ownGK, strongGK), move(ownGK, 207)}, "sold twice"},
		{"buy the same player twice", nil, []service.WhatIfMoveRequest{move(ownGK, strongGK), move(ownBenchGK, strongGK)}, "bought twice"},
		{"a fourth player from one club", addClubOneDEF, []service.WhatIfMoveRequest{move(ownDEF, clubOneDEF)}, "4 players from C01"},
		{"more than the money allows", nil, []service.WhatIfMoveRequest{move(ownDEF, expensiveDF)}, "over budget"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, rigOpts{mutate: tt.mutate})

			v := whatIf(t, r, service.WhatIfRequest{Moves: tt.moves})

			if v.Valid || !hasNote(v.Problems, tt.problem) {
				t.Errorf("valid %v problems %v, want one containing %q", v.Valid, v.Problems, tt.problem)
			}
			if v.Verdict != "" || len(v.Weeks) != 0 || len(v.Insights) != 0 {
				t.Errorf("nothing is worked out for an illegal plan: verdict %q weeks %d insights %q", v.Verdict, len(v.Weeks), v.Insights)
			}
		})
	}
}

func TestWhatIf_MoneyFromOneSaleCanPayForAnotherPurchase(t *testing.T) {
	// Selling a £10.1m defender's worth is not needed here: two cheap buys together are within budget,
	// but one of them alone would not be affordable were the other sale not counted.
	r := newRig(t, rigOpts{})

	v := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK), move(ownDEF, 202)}})

	if !v.Valid {
		t.Errorf("a plan paid for by its own sales must be valid: %v", v.Problems)
	}
}

func TestWhatIf_HitsAndTheFreeTransfersNextWeek(t *testing.T) {
	r := newRig(t, rigOpts{})
	moves1 := []service.WhatIfMoveRequest{move(ownGK, strongGK)}
	moves2 := []service.WhatIfMoveRequest{move(ownGK, strongGK), move(ownDEF, 202)}
	free := func(n int) *int { return &n }
	tests := []struct {
		name        string
		moves       []service.WhatIfMoveRequest
		free        *int
		hit, nextWk int
		insightEnds string
	}{
		{"within the free transfers, with some banked", moves1, free(3), 0, 3, "no points hit"},
		{"using exactly the free transfers", moves1, free(1), 0, 1, "no points hit"},
		{"banking is capped at five", moves1, free(5), 0, 5, "no points hit"},
		{"one transfer over", moves2, free(1), 4, 1, "4-point hit"},
		{"none free", moves1, free(0), 4, 1, "4-point hit"},
		{"two over", moves2, free(0), 8, 1, "8-point hit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := whatIf(t, r, service.WhatIfRequest{Moves: tt.moves, Free: tt.free})

			if v.Hit != tt.hit || v.FreeNextWeek != tt.nextWk || !hasInsight(v, tt.insightEnds) {
				t.Errorf("hit %d next week %d insights %q; want hit %d next week %d and %q", v.Hit, v.FreeNextWeek, v.Insights, tt.hit, tt.nextWk, tt.insightEnds)
			}
		})
	}
}

func TestWhatIf_AHitCanTurnAGoodMoveIntoABadOne(t *testing.T) {
	r := newRig(t, rigOpts{mutate: addWeakGK})
	none := 0
	small := []service.WhatIfMoveRequest{move(ownBenchGK, 213)} // Mkt12: a £7.7m goalkeeper scoring 5 a week, which the budget allows

	free := whatIf(t, r, service.WhatIfRequest{Moves: small})
	paid := whatIf(t, r, service.WhatIfRequest{Moves: small, Free: &none})

	if !free.Valid || !paid.Valid {
		t.Fatalf("the plan must be legal: %v %v", free.Problems, paid.Problems)
	}
	// The two figures are each rounded to a tenth, so allow for that.
	if d := free.NetPoints - paid.NetPoints; math.Abs(d-4) > 0.11 {
		t.Errorf("a 4-point hit must lower the net by 4: %v free, %v paid", free.NetPoints, paid.NetPoints)
	}
}

func TestWhatIf_ABenchPlayerWhoWouldNotStartIsFlagged(t *testing.T) {
	r := newRig(t, rigOpts{mutate: addWeakGK})

	v := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, weakGK)}})

	if !v.Valid {
		t.Fatal(v.Problems)
	}
	if !hasInsight(v, "Extra would not make the XI the model would pick") || !hasInsight(v, "Own01 is not in the XI the model would pick") {
		t.Errorf("insights = %q", v.Insights)
	}
}

func TestWhatIf_SuggestsABetterPickForTheSameSlotOnlyWhenItIsAffordableAndLegal(t *testing.T) {
	r := newRig(t, rigOpts{mutate: addWeakGK})

	v := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, weakGK)}})

	if !hasInsight(v, "For Own01's slot you could afford Mkt00") {
		t.Errorf("the 9-a-week goalkeeper is cheaper and better: insights = %q", v.Insights)
	}
	good := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}})
	if hasInsight(good, "you could afford") {
		t.Errorf("the best option must not be told it has a better one: %q", good.Insights)
	}
}

func TestWhatIf_FlagsAnInPlayerWhoIsNotFullyFit(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		for i := range w.players {
			if w.players[i].ID == strongGK {
				w.players[i].Status = "d"
			}
		}
	}})

	v := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}})

	if !v.Valid || !hasInsight(v, "Mkt00 is doubtful") {
		t.Errorf("valid %v insights %q", v.Valid, v.Insights)
	}
}

func TestWhatIf_NoMovesIsAnInvitationNotAnError(t *testing.T) {
	r := newRig(t, rigOpts{})

	v := whatIf(t, r, service.WhatIfRequest{})

	if v.Valid || len(v.Problems) != 0 || len(v.Insights) != 1 {
		t.Errorf("valid %v problems %v insights %q", v.Valid, v.Problems, v.Insights)
	}
	if v.BankBefore != "£2.0m" || len(v.Weeks) != 0 {
		t.Errorf("bank %q weeks %d", v.BankBefore, len(v.Weeks))
	}
}

func TestWhatIf_MalformedRequestsAreInputErrors(t *testing.T) {
	r := newRig(t, rigOpts{})
	many := make([]service.WhatIfMoveRequest, 16)
	for i := range many {
		many[i] = move(100+i+1, 200+i+1)
	}
	for name, req := range map[string]service.WhatIfRequest{
		"zero id":          {Moves: []service.WhatIfMoveRequest{move(0, strongGK)}},
		"negative id":      {Moves: []service.WhatIfMoveRequest{move(ownGK, -3)}},
		"horizon too big":  {Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}, Horizon: 11},
		"negative horizon": {Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}, Horizon: -1},
		"too many moves":   {Moves: many},
	} {
		_, err := r.svc.WhatIf(context.Background(), req)
		var in *service.InputError
		if !errors.As(err, &in) {
			t.Errorf("%s: err = %v, want an InputError", name, err)
		}
	}
}

func TestWhatIf_UsesTheViewersOwnSquad(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam})

	v, err := r.svc.WhatIf(ctx, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}})

	if err != nil || !v.Valid {
		t.Errorf("err %v valid %v problems %v", err, v.Valid, v.Problems)
	}
}
