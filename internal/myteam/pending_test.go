package myteam_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

// pendingFixture is the standard test team plus a market of players to buy.
// Squad players are PA..PO (ids 101-115); the market holds Zed, Yan, two Neds
// (an ambiguous name), and a defender Dee on a club that is already full.
func pendingFixture(t *testing.T) (myteam.Team, myteam.Finances, []fpl.Player) {
	t.Helper()
	c, players := fixture()
	team, err := myteam.Load(context.Background(), c, 4321, players)
	if err != nil {
		t.Fatal(err)
	}
	sell := map[int]int{}
	for _, p := range team.Squad.Players {
		sell[p.ID] = 68 // sells for a little less than the 70 market price
	}
	fin := myteam.Finances{Bank: 5, Free: myteam.FreeTransferState{Next: 2}, Sell: sell, Buy: map[int]int{}}

	market := append([]fpl.Player(nil), players...)
	market = append(market,
		fpl.Player{ID: 201, WebName: "Zed", TeamID: 7, Position: fpl.MID, NowCost: 60, Status: "a"},
		fpl.Player{ID: 202, WebName: "Yan", TeamID: 8, Position: fpl.DEF, NowCost: 55, Status: "a"},
		fpl.Player{ID: 203, WebName: "Ned", TeamID: 9, Position: fpl.FWD, NowCost: 70, Status: "a"},
		fpl.Player{ID: 204, WebName: "Ned", TeamID: 10, Position: fpl.GKP, NowCost: 45, Status: "a"},
		fpl.Player{ID: 205, WebName: "Dee", TeamID: 1, Position: fpl.DEF, NowCost: 50, Status: "a"}, // club 1 already has three
		fpl.Player{ID: 206, WebName: "Fox", TeamID: 11, Position: fpl.FWD, NowCost: 65, Status: "a", FirstName: "Fred", LastName: "Foxton"},
	)
	return team, fin, market
}

func TestParseSwaps(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    []myteam.SwapSpec
		wantErr string
	}{
		{"one swap", []string{"Havertz>Calvert-Lewin"}, []myteam.SwapSpec{{Out: "Havertz", In: "Calvert-Lewin"}}, ""},
		{"spaces are ignored", []string{" Cash  >  Vuskovic "}, []myteam.SwapSpec{{Out: "Cash", In: "Vuskovic"}}, ""},
		{"several in one value", []string{"A>B, C>D"}, []myteam.SwapSpec{{Out: "A", In: "B"}, {Out: "C", In: "D"}}, ""},
		{"several values", []string{"A>B", "C>D"}, []myteam.SwapSpec{{Out: "A", In: "B"}, {Out: "C", In: "D"}}, ""},
		{"names with spaces", []string{"João Pedro>Ollie Watkins"}, []myteam.SwapSpec{{Out: "João Pedro", In: "Ollie Watkins"}}, ""},
		{"no arrow", []string{"Havertz"}, nil, "Out>In"},
		{"nothing before the arrow", []string{">Vuskovic"}, nil, "Out>In"},
		{"nothing after the arrow", []string{"Cash>"}, nil, "Out>In"},
		{"empty", []string{"  "}, nil, "Out>In"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := myteam.ParseSwaps(tc.in)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestResolveSwaps(t *testing.T) {
	team, _, market := pendingFixture(t)

	tests := []struct {
		name     string
		spec     myteam.SwapSpec
		wantOut  int
		wantIn   int
		wantErr  string
		wantAlso string
	}{
		{"by web name", myteam.SwapSpec{Out: "PD", In: "Zed"}, 104, 201, "", ""},
		{"case does not matter", myteam.SwapSpec{Out: "pd", In: "ZED"}, 104, 201, "", ""},
		{"by full name", myteam.SwapSpec{Out: "PD", In: "Fred Foxton"}, 104, 206, "", ""},
		{"by last name", myteam.SwapSpec{Out: "PD", In: "Foxton"}, 104, 206, "", ""},
		{"by player id", myteam.SwapSpec{Out: "104", In: "206"}, 104, 206, "", ""},
		{"out player not in the squad", myteam.SwapSpec{Out: "Zed", In: "Yan"}, 0, 0, "not in your squad", "Zed"},
		{"in player already owned", myteam.SwapSpec{Out: "PD", In: "PE"}, 0, 0, "already in your squad", "PE"},
		{"unknown buy", myteam.SwapSpec{Out: "PD", In: "Nobody"}, 0, 0, "no player", "Nobody"},
		{"ambiguous buy lists the options", myteam.SwapSpec{Out: "PD", In: "Ned"}, 0, 0, "matches 2 players", "203"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := myteam.ResolveSwaps(team.Squad.Players, market, []myteam.SwapSpec{tc.spec})

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), tc.wantAlso) {
					t.Fatalf("err = %v, want one mentioning %q and %q", err, tc.wantErr, tc.wantAlso)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].Out.ID != tc.wantOut || got[0].In.ID != tc.wantIn {
				t.Errorf("got %+v, %v; want out %d in %d", got, err, tc.wantOut, tc.wantIn)
			}
		})
	}
}

func TestResolveSwaps_NoPlayerMayAppearTwice(t *testing.T) {
	team, _, market := pendingFixture(t)

	_, outTwice := myteam.ResolveSwaps(team.Squad.Players, market, []myteam.SwapSpec{{Out: "PD", In: "Yan"}, {Out: "PD", In: "Zed"}})
	_, inTwice := myteam.ResolveSwaps(team.Squad.Players, market, []myteam.SwapSpec{{Out: "PD", In: "Yan"}, {Out: "PE", In: "Yan"}})

	if outTwice == nil || !strings.Contains(outTwice.Error(), "twice") {
		t.Errorf("selling the same player twice: %v", outTwice)
	}
	if inTwice == nil || !strings.Contains(inTwice.Error(), "twice") {
		t.Errorf("buying the same player twice: %v", inTwice)
	}
}

func swap(t *testing.T, team myteam.Team, market []fpl.Player, specs ...myteam.SwapSpec) []myteam.Change {
	t.Helper()
	cs, err := myteam.ResolveSwaps(team.Squad.Players, market, specs)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestApply_PutsTheNewPlayerInTheOldPlayersSlot(t *testing.T) {
	team, _, market := pendingFixture(t)
	// PD (104) is a defender in the XI; Yan (202) is a defender on a new club.
	changes := swap(t, team, market, myteam.SwapSpec{Out: "PD", In: "Yan"})

	got, err := myteam.Apply(team, changes)

	if err != nil {
		t.Fatal(err)
	}
	if got.Squad.Players[3].ID != 202 {
		t.Errorf("slot 4 holds %d, want the new defender 202 in the sold player's place", got.Squad.Players[3].ID)
	}
	if !slices.Contains(got.Squad.Starting, 202) || slices.Contains(got.Squad.Starting, 104) {
		t.Errorf("starting XI %v should have 202 in and 104 out", got.Squad.Starting)
	}
	// The original is untouched.
	if team.Squad.Players[3].ID != 104 {
		t.Error("Apply modified the team it was given")
	}
}

func TestApply_TheArmbandStaysWithTheSlot(t *testing.T) {
	team, _, market := pendingFixture(t)
	changes := swap(t, team, market, myteam.SwapSpec{Out: "PJ", In: "Fox"}) // PJ (110) is the captain, a forward; Fox is a forward

	got, err := myteam.Apply(team, changes)

	if err != nil {
		t.Fatal(err)
	}
	// FPL clears the armband when the captain is sold; the new player is assumed to take it
	// so that the squad stays valid, and `lineup` re-picks the real captain anyway.
	if got.Squad.Captain != 206 {
		t.Errorf("captain = %d, want the incoming 206", got.Squad.Captain)
	}
}

func TestApply_AcceptsASwapPairThatKeepsTheSquadShape(t *testing.T) {
	team, _, market := pendingFixture(t)
	// A defender out for a midfielder, and a midfielder out for a defender: fine together.
	changes := swap(t, team, market, myteam.SwapSpec{Out: "PD", In: "Zed"}, myteam.SwapSpec{Out: "PI", In: "Yan"})

	if _, err := myteam.Apply(team, changes); err != nil {
		t.Errorf("a balanced pair of cross-position swaps was rejected: %v", err)
	}
}

func TestApply_RejectsSquadsThatBreakTheRules(t *testing.T) {
	team, _, market := pendingFixture(t)
	tests := []struct {
		name    string
		spec    myteam.SwapSpec
		wantMsg string
	}{
		{"a fourth player from one club", myteam.SwapSpec{Out: "PD", In: "Dee"}, "club"},
		{"a defender for a midfielder alone leaves the wrong split", myteam.SwapSpec{Out: "PD", In: "Zed"}, "position"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changes := swap(t, team, market, tc.spec)

			_, err := myteam.Apply(team, changes)

			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantMsg) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantMsg)
			}
		})
	}
}

func TestApplyFinances_BankSellPricesAndFreeTransfers(t *testing.T) {
	team, fin, market := pendingFixture(t)
	changes := swap(t, team, market, myteam.SwapSpec{Out: "PD", In: "Yan"}) // sell 68, buy 55

	got := myteam.ApplyFinances(fin, changes)

	if got.Bank != 5+68-55 {
		t.Errorf("bank = %d, want %d: the old bank plus the sale minus the purchase", got.Bank, 5+68-55)
	}
	if got.Sell[202] != 55 {
		t.Errorf("a player bought today sells for what he cost, %d; got %d", 55, got.Sell[202])
	}
	if _, still := got.Sell[104]; still {
		t.Error("the sold player must leave the selling-price table")
	}
	if got.Free.Next != 1 || got.PendingTransfers != 1 || got.PendingHit != 0 {
		t.Errorf("free %d pending %d hit %d; want 1 free left after using one of two, no hit", got.Free.Next, got.PendingTransfers, got.PendingHit)
	}
	if fin.Bank != 5 || fin.Sell[104] != 68 {
		t.Error("ApplyFinances modified the finances it was given")
	}
}

func TestApplyFinances_ExtraTransfersAlreadyCostAHit(t *testing.T) {
	team, fin, market := pendingFixture(t)
	fin.Free.Next = 1
	changes := swap(t, team, market,
		myteam.SwapSpec{Out: "PD", In: "Yan"},
		myteam.SwapSpec{Out: "PJ", In: "Fox"},
		myteam.SwapSpec{Out: "PK", In: "203"}, // 203 is a forward (named by id because "Ned" is ambiguous)
	)

	got := myteam.ApplyFinances(fin, changes)

	// One free transfer covers one of the three; the other two are four points each.
	if got.PendingTransfers != 3 || got.PendingHit != 8 || got.Free.Next != 0 {
		t.Errorf("pending %d hit %d free %d; want 3, 8, 0", got.PendingTransfers, got.PendingHit, got.Free.Next)
	}
}

func TestDescribeChanges(t *testing.T) {
	team, _, market := pendingFixture(t)
	changes := swap(t, team, market, myteam.SwapSpec{Out: "PD", In: "Yan"}, myteam.SwapSpec{Out: "PJ", In: "Fox"})

	got := myteam.DescribeChanges(changes)

	if got != "PD → Yan, PJ → Fox" {
		t.Errorf("got %q", got)
	}
	if myteam.DescribeChanges(nil) != "" {
		t.Error("no changes should describe as nothing")
	}
}
