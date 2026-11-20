package service_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func made(pairs ...string) []myteam.SwapSpec {
	out := make([]myteam.SwapSpec, len(pairs))
	for i, p := range pairs {
		sold, bought, _ := strings.Cut(p, ">")
		out[i] = myteam.SwapSpec{Out: sold, In: bought}
	}
	return out
}

func squadNames(v service.MyTeamView) []string {
	return append(names(v.Starting), names(v.Bench)...)
}

// The squad is Own01-Own15 (ids 101-115, £6.0m each; 101 and 112 the goalkeepers, 110, 111 and 115 the forwards) with
// £2.0m in the bank; the market is Mkt00-Mkt29 (ids 201-230, Mkt00 a £4.5m goalkeeper, Mkt05 an
// £8.5m forward, Mkt11 a £6.9m forward).
func TestMyTeam_EnteredTransfersAreAppliedAndChecked(t *testing.T) {
	tests := []struct {
		name       string
		made       []myteam.SwapSpec
		wantSource string
		wantIn     []string // players the squad must then hold
		wantOut    []string // players it must no longer hold
		wantBank   string
		wantNote   string
		wantErr    string // part of the refusal; empty when it must succeed
	}{
		{
			name: "a like-for-like transfer is applied, with the money",
			made: made("101>201"), wantSource: "entered",
			wantIn: []string{"Mkt00"}, wantOut: []string{"Own01"}, wantBank: "£3.5m",
			wantNote: "plus the 1 transfer you entered (Own01 → Mkt00)",
		},
		{
			name: "two transfers at once",
			made: made("101>201", "110>206"), wantSource: "entered",
			wantIn: []string{"Mkt00", "Mkt05"}, wantOut: []string{"Own01", "Own10"}, wantBank: "£1.0m",
			wantNote: "the 2 transfers you entered",
		},
		{
			name:    "one that goes over budget is refused",
			made:    made("110>206", "111>212"),
			wantErr: "over budget",
		},
		{
			name:    "selling a player who is not in the squad is refused",
			made:    made("150>201"),
			wantErr: "do not fit your squad",
		},
		{
			name:    "a swap across positions breaks the squad's shape and is refused",
			made:    made("101>202"),
			wantErr: "do not fit your squad",
		},
		{
			name: "a transfer FPL's squad already shows (after the deadline) is not applied twice",
			made: made("199>101"), wantSource: "public",
			wantIn: []string{"Own01"}, wantBank: "£2.0m",
			wantNote: "already in FPL's squad",
		},
		{
			name: "nothing entered: the public squad, saying how to include recent transfers",
			made: nil, wantSource: "public",
			wantIn: []string{"Own01"}, wantBank: "£2.0m",
			wantNote: "enter the transfers you have made",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOpts{})
			ctx := service.WithViewer(context.Background(), service.Viewer{Made: tc.made})

			got, err := r.svc.MyTeam(ctx)

			if tc.wantErr != "" {
				var ie *service.InputError
				if !errors.As(err, &ie) || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want an input error mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Source != tc.wantSource || got.Bank != tc.wantBank {
				t.Errorf("source %q bank %q, want %q %q", got.Source, got.Bank, tc.wantSource, tc.wantBank)
			}
			have := squadNames(got)
			if len(have) != 15 {
				t.Fatalf("squad has %d players", len(have))
			}
			for _, n := range tc.wantIn {
				if !slices.Contains(have, n) {
					t.Errorf("%s should be in the squad %v", n, have)
				}
			}
			for _, n := range tc.wantOut {
				if slices.Contains(have, n) {
					t.Errorf("%s should have left the squad %v", n, have)
				}
			}
			if !hasNote(got.Notes, tc.wantNote) {
				t.Errorf("notes %v should mention %q", got.Notes, tc.wantNote)
			}
		})
	}
}

func TestMyTeam_EnteredTransfersSpendFreeTransfersAndCountTowardsAHit(t *testing.T) {
	r := newRig(t, rigOpts{})
	before, err := r.svc.MyTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithViewer(context.Background(), service.Viewer{Made: made("101>201")})
	after, err := r.svc.MyTeam(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.FreeTransfers != max(before.FreeTransfers-1, 0) || after.TransfersMade != before.TransfersMade+1 {
		t.Errorf("free %d→%d, made %d→%d: one transfer should use one free transfer", before.FreeTransfers, after.FreeTransfers, before.TransfersMade, after.TransfersMade)
	}
	if after.AsOf != "the GW7 deadline, plus the transfers you entered" {
		t.Errorf("squad_as_of = %q", after.AsOf)
	}
}

func TestMyTeam_EnteredTransfersAreIgnoredForALiveSquad(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(47 * time.Minute))})
	ctx := service.WithViewer(context.Background(), service.Viewer{Made: made("101>201")})

	got, err := r.svc.MyTeam(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "live" || slices.Contains(squadNames(got), "Mkt00") {
		t.Errorf("source %q squad %v: a live squad must not have entered transfers applied", got.Source, squadNames(got))
	}
	if !hasNote(got.Notes, "not applied: the live squad already includes") {
		t.Errorf("notes %v should say why they were not applied", got.Notes)
	}
}

func TestMyTeam_SquadsWithAndWithoutEnteredTransfersAreCachedApart(t *testing.T) {
	r := newRig(t, rigOpts{})
	with := service.WithViewer(context.Background(), service.Viewer{Made: made("101>201")})
	if _, err := r.svc.MyTeam(with); err != nil {
		t.Fatal(err)
	}
	plain, err := r.svc.MyTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plain.Source != "public" || slices.Contains(squadNames(plain), "Mkt00") {
		t.Errorf("a request without entered transfers got the squad with them: %q %v", plain.Source, squadNames(plain))
	}
}
