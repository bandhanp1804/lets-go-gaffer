package main

import (
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

// transferFixture is a team with one sensible suggestion: sell player 103 (bought
// high, now cheaper) for a better one.
func transferFixture() transfersView {
	team, teams := legalTeam()
	team.History.Bank = 2
	team.Squad.Players[2].WebName = "Hughes"
	team.Squad.Players[2].Form = "0.5"
	buy, sell := map[int]int{}, map[int]int{}
	for _, p := range team.Squad.Players {
		buy[p.ID], sell[p.ID] = p.NowCost, p.NowCost
	}
	buy[103], sell[103] = 75, 70
	in := fpl.Player{ID: 900, WebName: "Foden", TeamID: 2, Position: team.Squad.Players[2].Position, NowCost: 72, Form: "6.0"}
	return transfersView{
		Team: team, Teams: teams, Horizon: 4,
		Fin:  myteam.Finances{Bank: 2, Free: myteam.FreeTransferState{Next: 1}, Buy: buy, Sell: sell},
		Free: 1, FreeSource: "replayed from your history",
		Suggestions: []transfers.Suggestion{{
			Moves:     []transfers.Move{{Out: team.Squad.Players[2], In: in, SellPrice: 70}},
			Transfers: 1, Hit: 0, CostChange: 2, BankAfter: 0, ScoreGain: 1.9, Net: 7.6,
		}},
	}
}

func TestPrintTransfers_ShowsTheMoveItsMoneyAndItsWorth(t *testing.T) {
	var b strings.Builder

	printTransfers(&b, transferFixture())

	out := b.String()
	for _, want := range []string{
		"Gaffer FC", "Free transfers: 1", "replayed from your history", "bank £0.2m", "4 gameweeks",
		"OUT Hughes", "sells £7.0m", "IN  Foden", "£7.2m", "bank after £0.0m",
		"+7.6 net", "1 transfer, no hit", "+1.9 a gameweek",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTransfers_ANonZeroHitIsStatedPlainly(t *testing.T) {
	v := transferFixture()
	v.Free = 0
	v.Suggestions[0].Hit = 4
	v.Suggestions[0].Net = 3.6
	var b strings.Builder

	printTransfers(&b, v)

	if !strings.Contains(b.String(), "-4 hit") {
		t.Errorf("a costly move must say what it costs:\n%s", b.String())
	}
}

func TestPrintTransfers_NothingWorthDoingSaysSoAndWhatBankingMeans(t *testing.T) {
	v := transferFixture()
	v.Suggestions = nil
	v.Free = 1
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	if !strings.Contains(out, "No transfer beats keeping your squad") {
		t.Errorf("want a keep-your-squad message:\n%s", out)
	}
	if !strings.Contains(out, "2 free transfers next week") { // banking one more
		t.Errorf("want the banked count for next week:\n%s", out)
	}
}

func TestPrintTransfers_BankedTransfersStopAtFive(t *testing.T) {
	v := transferFixture()
	v.Suggestions, v.Free = nil, 5
	var b strings.Builder

	printTransfers(&b, v)

	if !strings.Contains(b.String(), "5 free transfers next week") || strings.Contains(b.String(), "6 free") {
		t.Errorf("banking is capped at five:\n%s", b.String())
	}
}

func TestPrintTransfers_WarnsWhenTheFreeTransferReplayDisagreesWithFPL(t *testing.T) {
	v := transferFixture()
	v.Fin.Free = myteam.FreeTransferState{Next: 1, Mismatches: []int{3, 5}}
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	if !strings.Contains(out, "could not be verified") || !strings.Contains(out, "3, 5") || !strings.Contains(out, "-free") {
		t.Errorf("want a warning naming the gameweeks and the -free override:\n%s", out)
	}
}

func TestPrintTransfers_Wildcard(t *testing.T) {
	v := transferFixture()
	v.Wildcard = true
	v.Suggestions[0].Net = v.Suggestions[0].ScoreGain
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	if !strings.Contains(out, "Wildcard") || !strings.Contains(out, "no hit") {
		t.Errorf("wildcard output should say there is no limit and no hit:\n%s", out)
	}
	if strings.Contains(out, "Free transfers:") {
		t.Errorf("free transfers are irrelevant under a wildcard:\n%s", out)
	}
}

func TestPrintTransfers_PricesTableLetsYouCheckSellingPricesAgainstFPL(t *testing.T) {
	v := transferFixture()
	v.ShowPrices = true
	var b strings.Builder

	printTransfers(&b, v)

	out := b.String()
	if !strings.Contains(out, "Selling prices") || !strings.Contains(out, "Hughes") || !strings.Contains(out, "£7.5m") || !strings.Contains(out, "£7.0m") {
		t.Errorf("want the buy and sell price of each player:\n%s", out)
	}
}

func TestPluralFreeTransfers(t *testing.T) {
	for n, want := range map[int]string{0: "0 free transfers", 1: "1 free transfer", 2: "2 free transfers"} {
		if got := freeTransfers(n); got != want {
			t.Errorf("freeTransfers(%d) = %q, want %q", n, got, want)
		}
	}
}
