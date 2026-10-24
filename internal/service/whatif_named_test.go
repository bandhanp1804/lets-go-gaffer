package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func named(out, in string) service.NamedMove { return service.NamedMove{Out: out, In: in} }

func rename(w *world, id int, name string) {
	for i := range w.players {
		if w.players[i].ID == id {
			w.players[i].WebName = name
		}
	}
}

func whatIfNamed(t *testing.T, r *rig, moves ...service.NamedMove) (service.WhatIfView, error) {
	t.Helper()
	return r.svc.WhatIfNamed(context.Background(), service.WhatIfNamedRequest{Moves: moves})
}

func TestWhatIfNamed_SameAnswerAsByIdWhenTheNamesAreClear(t *testing.T) {
	r := newRig(t, rigOpts{})

	byName, err := whatIfNamed(t, r, named("Own01", "Mkt00"))
	byID := whatIf(t, r, service.WhatIfRequest{Moves: []service.WhatIfMoveRequest{move(ownGK, strongGK)}})

	if err != nil {
		t.Fatal(err)
	}
	if !byName.Valid || byName.NetPoints != byID.NetPoints || byName.Moves[0].In.Name != "Mkt00" || byName.Moves[0].Out.Name != "Own01" {
		t.Errorf("by name %+v, by id %+v", byName.NetPoints, byID.NetPoints)
	}
}

func TestWhatIfNamed_CaseAccentsPunctuationAndPartsOfANameAreForgiven(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		rename(w, strongGK, "N.Groß-Müller") // a market goalkeeper with every awkward character
		rename(w, ownGK, "E.Le Fée")
	}})
	for _, tt := range []struct{ out, in string }{
		{"e.le fée", "n.gross-muller"},
		{"LE FEE", "gross muller"},
		{"Le Fée", "Groß"},
		{"le fee", "MULLER"},
	} {
		v, err := whatIfNamed(t, r, named(tt.out, tt.in))
		if err != nil || len(v.Moves) != 1 || v.Moves[0].In.Name != "N.Groß-Müller" || v.Moves[0].Out.Name != "E.Le Fée" {
			t.Errorf("%q -> %q: err %v moves %+v", tt.out, tt.in, err, v.Moves)
		}
	}
}

func TestWhatIfNamed_AnExactNameBeatsAPartialOne(t *testing.T) {
	// "Mkt1" is a part of Mkt10..Mkt19, but there is a player named exactly that.
	r := newRig(t, rigOpts{mutate: func(w *world) { rename(w, 225, "Mkt1") }}) // 225 is a goalkeeper (index 24 % 6 = 0)

	v, err := whatIfNamed(t, r, named("Own01", "Mkt1"))

	if err != nil || v.Moves[0].In.ID != 225 {
		t.Errorf("err %v moves %+v", err, v.Moves)
	}
}

func TestWhatIfNamed_ANameShared_ByPlayersOfDifferentPositionsIsNarrowedByTheSale(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		addPlayer(w, 300, fpl.GKP, 6, 40, 1) // both are called Extra
		addPlayer(w, 301, fpl.DEF, 7, 45, 3)
	}})

	gk, err1 := whatIfNamed(t, r, named("Own01", "Extra"))
	df, err2 := whatIfNamed(t, r, named("Own02", "Extra"))

	if err1 != nil || err2 != nil {
		t.Fatalf("%v %v", err1, err2)
	}
	if gk.Moves[0].In.ID != 300 || df.Moves[0].In.ID != 301 {
		t.Errorf("selling a goalkeeper should buy the goalkeeper Extra, and a defender the defender Extra: %d, %d", gk.Moves[0].In.ID, df.Moves[0].In.ID)
	}
}

func TestWhatIfNamed_ASharedNameInOnePositionNeedsAClub_AndTheErrorSaysWhich(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		addPlayer(w, 300, fpl.GKP, 6, 40, 1)
		addPlayer(w, 302, fpl.GKP, 7, 41, 1)
	}})

	_, err := whatIfNamed(t, r, named("Own01", "Extra"))

	var in *service.InputError
	if !errors.As(err, &in) || !strings.Contains(err.Error(), "C06") || !strings.Contains(err.Error(), "C07") || !strings.Contains(err.Error(), "club") {
		t.Fatalf("err = %v, want an InputError that lists both clubs and asks for one", err)
	}

	v, err := whatIfNamed(t, r, service.NamedMove{Out: "Own01", In: "Extra", InTeam: "C07"})
	if err != nil || v.Moves[0].In.ID != 302 {
		t.Errorf("with the club given: err %v moves %+v", err, v.Moves)
	}
	if _, err := whatIfNamed(t, r, service.NamedMove{Out: "Own01", In: "Extra", InTeam: "ZZZ"}); !errors.As(err, &in) {
		t.Errorf("an unknown club must be an input error, got %v", err)
	}
}

func TestWhatIfNamed_ErrorsTellTheCallerWhatToDo(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		rename(w, 103, "Silva")
		rename(w, 104, "Silva")
	}})
	var in *service.InputError
	tests := []struct {
		name     string
		move     service.NamedMove
		mentions []string
	}{
		{"not one of yours", named("Mkt05", "Mkt00"), []string{"none of your players", "Own01", "(15 players)"}},
		{"two of yours", named("Silva", "Mkt00"), []string{"any of your players", "full name"}},
		{"nobody by that name", named("Own01", "Nobody"), []string{"no player is called", "Nobody"}},
		{"empty sale", named("  ", "Mkt00"), []string{"name of the player to sell"}},
		{"empty buy", named("Own01", ""), []string{"name of the player to buy"}},
	}
	for _, tt := range tests {
		_, err := whatIfNamed(t, r, tt.move)
		if !errors.As(err, &in) {
			t.Errorf("%s: err = %v, want an InputError", tt.name, err)
			continue
		}
		for _, m := range tt.mentions {
			if !strings.Contains(err.Error(), m) {
				t.Errorf("%s: %q should mention %q", tt.name, err, m)
			}
		}
	}
}

func TestWhatIfNamed_ARuleBreakIsAProblemInTheViewNotAnError(t *testing.T) {
	r := newRig(t, rigOpts{})

	v, err := whatIfNamed(t, r, named("Own01", "Mkt01")) // a goalkeeper for a defender

	if err != nil {
		t.Fatal(err)
	}
	if v.Valid || !hasNote(v.Problems, "like for like") {
		t.Errorf("valid %v problems %v", v.Valid, v.Problems)
	}
}

func TestWhatIfNamed_ManyMovesAndTheSameLimits(t *testing.T) {
	r := newRig(t, rigOpts{})
	moves := make([]service.NamedMove, 16)
	for i := range moves {
		moves[i] = named("Own01", "Mkt00")
	}
	var in *service.InputError
	if _, err := whatIfNamed(t, r, moves...); !errors.As(err, &in) {
		t.Errorf("err = %v", err)
	}
	v, err := whatIfNamed(t, r)
	if err != nil || v.Valid || len(v.Insights) != 1 {
		t.Errorf("no moves is an invitation: err %v valid %v insights %v", err, v.Valid, v.Insights)
	}
}
