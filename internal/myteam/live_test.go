package myteam_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// liveFixture is the standard test team as FPL's logged-in endpoint would report it
// now: the same 15 players, with FPL's own prices and this week's transfer position.
func liveFixture(t *testing.T) (myteam.Team, *fpl.MyTeam, []fpl.Player) {
	t.Helper()
	c, players := fixture()
	team, err := myteam.Load(context.Background(), c, 4321, players)
	if err != nil {
		t.Fatal(err)
	}
	live := &fpl.MyTeam{
		Transfers: fpl.MyTransfers{Cost: 4, Status: "cost", Limit: 1, Made: 2, Bank: 9, Value: 1003},
		Chips: []fpl.MyChip{
			{Name: "wildcard", Status: "available"}, {Name: "freehit", Status: "played"},
			{Name: "bboost", Status: "available"},
		},
	}
	for _, p := range c.picks.Picks {
		live.Picks = append(live.Picks, fpl.MyPick{
			Element: p.Element, Position: p.Position, Multiplier: p.Multiplier,
			IsCaptain: p.IsCaptain, IsViceCaptain: p.IsViceCaptain, ElementType: p.ElementType,
			SellingPrice: 60 + p.Position, PurchasePrice: 55 + p.Position, // distinct per player
		})
	}
	return team, live, players
}

func TestWithLiveSquad_BuildsTheSquadAndTheExactFinances(t *testing.T) {
	team, live, players := liveFixture(t)

	got, fin, err := myteam.WithLiveSquad(team, live, players)

	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, p := range got.Squad.Players {
		ids = append(ids, p.ID)
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115}; !slices.Equal(ids, want) {
		t.Errorf("players = %v, want pick order", ids)
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111}; !slices.Equal(got.Squad.Starting, want) {
		t.Errorf("starting = %v, want slots 1-11", got.Squad.Starting)
	}
	if got.Squad.Captain != 110 || got.Squad.ViceCaptain != 106 {
		t.Errorf("captain/vice = %d/%d, want 110/106", got.Squad.Captain, got.Squad.ViceCaptain)
	}
	if got.Entry != team.Entry || got.Event != team.Event || got.History.Points != team.History.Points ||
		got.History.OverallRank != team.History.OverallRank {
		t.Error("the manager's name, gameweek, points, and rank must carry over from the public data")
	}
	// The public bank and value are as of the last deadline; showing them beside the live
	// ones would print two different banks. The header uses FPL's live figures instead.
	if got.History.Bank != 9 || got.History.Value != 1003 {
		t.Errorf("header bank/value = %d/%d, want the live 9/1003", got.History.Bank, got.History.Value)
	}
	if got.History.EventTransfers != 0 || got.History.EventTransfersCost != 0 {
		t.Errorf("last gameweek's transfers (%d, -%d) belong to the past, not this week's live position",
			got.History.EventTransfers, got.History.EventTransfersCost)
	}
	if team.History.Bank == 9 {
		t.Error("the team given was modified")
	}

	if fin.Bank != 9 {
		t.Errorf("bank = %d, want FPL's 9", fin.Bank)
	}
	// FPL's selling prices are used as given, not modelled.
	if fin.Sell[101] != 61 || fin.Sell[115] != 75 || fin.Buy[101] != 56 || len(fin.Sell) != 15 {
		t.Errorf("prices: sell[101]=%d sell[115]=%d buy[101]=%d count=%d", fin.Sell[101], fin.Sell[115], fin.Buy[101], len(fin.Sell))
	}
	if err := optimizer.ValidateWithBudget(got.Squad, optimizer.NoBudgetLimit); err != nil {
		t.Errorf("a squad FPL reports as real failed our rules: %v", err)
	}
}

func TestWithLiveSquad_FreeTransfersLeftAreTheAllowanceMinusThoseMade(t *testing.T) {
	tests := []struct {
		name                           string
		limit, made, cost              int
		wantFree, wantPending, wantHit int
	}{
		{"two made against one free: nothing left, one hit", 1, 2, 4, 0, 2, 4},
		{"one made against two free: one left, no hit", 2, 1, 0, 1, 1, 0},
		{"none made", 3, 0, 0, 3, 0, 0},
		{"exactly the allowance", 2, 2, 0, 0, 2, 0},
		{"far over the allowance", 1, 4, 12, 0, 4, 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			team, live, players := liveFixture(t)
			live.Transfers.Limit, live.Transfers.Made, live.Transfers.Cost = tc.limit, tc.made, tc.cost

			_, fin, err := myteam.WithLiveSquad(team, live, players)

			if err != nil {
				t.Fatal(err)
			}
			if fin.Free.Next != tc.wantFree || fin.PendingTransfers != tc.wantPending || fin.PendingHit != tc.wantHit {
				t.Errorf("free %d pending %d hit %d; want %d, %d, %d", fin.Free.Next, fin.PendingTransfers, fin.PendingHit, tc.wantFree, tc.wantPending, tc.wantHit)
			}
		})
	}
}

func TestWithLiveSquad_ListsOnlyTheChipsStillAvailable(t *testing.T) {
	team, live, players := liveFixture(t)

	_, fin, err := myteam.WithLiveSquad(team, live, players)

	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"wildcard", "bboost"}; !slices.Equal(fin.AvailableChips, want) {
		t.Errorf("available chips = %v, want %v (the played free hit is not offered)", fin.AvailableChips, want)
	}
}

func TestWithLiveSquad_SortsPicksByPosition(t *testing.T) {
	team, live, players := liveFixture(t)
	slices.Reverse(live.Picks)

	got, _, err := myteam.WithLiveSquad(team, live, players)

	if err != nil {
		t.Fatal(err)
	}
	if want := []int{101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111}; !slices.Equal(got.Squad.Starting, want) {
		t.Errorf("starting = %v", got.Squad.Starting)
	}
}

func TestWithLiveSquad_DoesNotModifyItsInputs(t *testing.T) {
	team, live, players := liveFixture(t)
	picksBefore := slices.Clone(live.Picks)
	firstBefore := team.Squad.Players[0].ID

	if _, _, err := myteam.WithLiveSquad(team, live, players); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(live.Picks, picksBefore) || team.Squad.Players[0].ID != firstBefore {
		t.Error("WithLiveSquad modified what it was given")
	}
}

func TestWithLiveSquad_Failures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(live *fpl.MyTeam, players *[]fpl.Player)
		wantMsg string
	}{
		{"fewer than 15 picks", func(l *fpl.MyTeam, _ *[]fpl.Player) { l.Picks = l.Picks[:14] }, "14"},
		{"a player the database does not know", func(_ *fpl.MyTeam, p *[]fpl.Player) { *p = slices.Delete(*p, 4, 5) }, "gaffer sync"},
		{"no captain", func(l *fpl.MyTeam, _ *[]fpl.Player) { l.Picks[9].IsCaptain = false }, "captain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			team, live, players := liveFixture(t)
			tc.mutate(live, &players)

			_, _, err := myteam.WithLiveSquad(team, live, players)

			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantMsg)
			}
		})
	}
}
