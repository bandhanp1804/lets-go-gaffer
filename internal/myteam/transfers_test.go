package myteam_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

// gw builds a gameweek record: event, transfers made, points lost to hits.
func gw(event, transfers, cost int) fpl.GameweekHistory {
	return fpl.GameweekHistory{Event: event, EventTransfers: transfers, EventTransfersCost: cost}
}

func TestFreeTransfers(t *testing.T) {
	tests := []struct {
		name       string
		history    fpl.History
		wantNext   int
		wantMisses []int
	}{
		{
			// A real manager's first 7 gameweeks (event, transfers, hit): the model must
			// reproduce every hit FPL reported, which is 0 each time.
			name: "a real season, including banking and using two",
			history: fpl.History{Current: []fpl.GameweekHistory{
				gw(1, 0, 0), gw(2, 1, 0), gw(3, 1, 0), gw(4, 0, 0), gw(5, 2, 0), gw(6, 0, 0), gw(7, 2, 0),
			}},
			wantNext: 1,
		},
		{
			name:     "nothing before the first gameweek",
			history:  fpl.History{},
			wantNext: 1,
		},
		{
			name:     "one free transfer arrives each week",
			history:  fpl.History{Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 1, 0), gw(3, 1, 0)}},
			wantNext: 1,
		},
		{
			name:     "unused transfers bank",
			history:  fpl.History{Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 0, 0), gw(3, 0, 0)}},
			wantNext: 3, // 1 banked from GW2, 1 from GW3, plus the new one
		},
		{
			name: "banking stops at five",
			history: fpl.History{Current: []fpl.GameweekHistory{
				gw(1, 0, 0), gw(2, 0, 0), gw(3, 0, 0), gw(4, 0, 0), gw(5, 0, 0), gw(6, 0, 0), gw(7, 0, 0), gw(8, 0, 0),
			}},
			wantNext: 5,
		},
		{
			name:     "extra transfers cost four points each and empty the bank",
			history:  fpl.History{Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 3, 8)}}, // 1 free + 2 hits
			wantNext: 1,
		},
		{
			name:       "a reported hit the model cannot explain is flagged, not hidden",
			history:    fpl.History{Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 3, 4)}}, // should be 8
			wantNext:   1,
			wantMisses: []int{2},
		},
		{
			name: "a wildcard week uses no free transfers",
			history: fpl.History{
				Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 0, 0), gw(3, 9, 0)},
				Chips:   []fpl.ChipUse{{Name: "wildcard", Event: 3}},
			},
			wantNext: 3, // GW2 banked 1; GW3 had 2 and the wildcard spared them; GW4 adds one
		},
		{
			name: "a free hit week uses no free transfers either",
			history: fpl.History{
				Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 11, 0)},
				Chips:   []fpl.ChipUse{{Name: "freehit", Event: 2}},
			},
			wantNext: 2,
		},
		{
			name: "other chips do not change transfers",
			history: fpl.History{
				Current: []fpl.GameweekHistory{gw(1, 0, 0), gw(2, 1, 0)},
				Chips:   []fpl.ChipUse{{Name: "bboost", Event: 2}},
			},
			wantNext: 1,
		},
		{
			name:     "gameweeks may arrive in any order",
			history:  fpl.History{Current: []fpl.GameweekHistory{gw(3, 1, 0), gw(1, 0, 0), gw(2, 0, 0)}},
			wantNext: 2, // GW2 banked 1; GW3 had 2 and used 1, leaving 1; GW4 adds one
		},
		{
			name:     "a manager who joined late treats their first gameweek as unlimited",
			history:  fpl.History{Current: []fpl.GameweekHistory{gw(4, 15, 0), gw(5, 1, 0)}},
			wantNext: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := myteam.FreeTransfers(tc.history)

			if got.Next != tc.wantNext {
				t.Errorf("Next = %d, want %d", got.Next, tc.wantNext)
			}
			if !slices.Equal(got.Mismatches, tc.wantMisses) {
				t.Errorf("Mismatches = %v, want %v", got.Mismatches, tc.wantMisses)
			}
		})
	}
}

func TestFreeTransfers_DoesNotReorderTheCallersHistory(t *testing.T) {
	h := fpl.History{Current: []fpl.GameweekHistory{gw(3, 1, 0), gw(1, 0, 0), gw(2, 0, 0)}}

	myteam.FreeTransfers(h)

	if h.Current[0].Event != 3 {
		t.Error("FreeTransfers sorted the caller's slice in place")
	}
}

func TestSellPrice(t *testing.T) {
	// You keep half of any rise, rounded down to 0.1m. A fall costs you in full.
	tests := []struct{ buy, now, want int }{
		{50, 50, 50},
		{50, 51, 50}, // a 0.1m rise is half a tenth: rounds down to nothing
		{50, 52, 51},
		{50, 53, 51}, // 1.5 rounds down to 1
		{50, 54, 52},
		{155, 157, 156},
		{50, 49, 49}, // price fell: you lose all of it
		{45, 43, 43},
	}
	for _, tc := range tests {
		if got := myteam.SellPrice(tc.buy, tc.now); got != tc.want {
			t.Errorf("SellPrice(buy %d, now %d) = %d, want %d", tc.buy, tc.now, got, tc.want)
		}
	}
}

func transferIn(event, id, cost int, at string) fpl.Transfer {
	ts, _ := time.Parse(time.RFC3339, at)
	return fpl.Transfer{Event: event, PlayerIn: id, PlayerInCost: cost, Time: ts}
}

func TestBuyPrices(t *testing.T) {
	squad := []int{1, 2, 3}
	initial := map[int]int{1: 60, 2: 45, 3: 80}
	transfers := []fpl.Transfer{
		transferIn(5, 2, 50, "2026-09-12T16:00:00Z"),
		transferIn(7, 2, 55, "2026-10-10T16:00:00Z"), // bought again later at a different price
		transferIn(3, 9, 70, "2026-08-28T16:00:00Z"), // a player no longer in the squad
	}

	got, err := myteam.BuyPrices(squad, transfers, initial)

	if err != nil {
		t.Fatal(err)
	}
	want := map[int]int{
		1: 60, // never transferred in: the price he started at
		2: 55, // the most recent purchase counts
		3: 80,
	}
	for id, price := range want {
		if got[id] != price {
			t.Errorf("buy price of %d = %d, want %d", id, got[id], price)
		}
	}
}

func TestBuyPrices_UnknownPriceIsAnErrorNotAGuess(t *testing.T) {
	// A wrong buy price quietly makes every affordability check wrong.
	_, err := myteam.BuyPrices([]int{1, 2}, nil, map[int]int{1: 60})

	if err == nil || !strings.Contains(err.Error(), "player 2") {
		t.Errorf("err = %v, want one naming player 2", err)
	}
}

func TestSellPrices(t *testing.T) {
	players := []fpl.Player{{ID: 1, NowCost: 53}, {ID: 2, NowCost: 43}}

	got := myteam.SellPrices(players, map[int]int{1: 50, 2: 45})

	if got[1] != 51 || got[2] != 43 {
		t.Errorf("sell prices = %v, want map[1:51 2:43]", got)
	}
}
