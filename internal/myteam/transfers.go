package myteam

import (
	"context"
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

// FreeTransferState is the result of replaying a season's transfers.
type FreeTransferState struct {
	// Next is how many free transfers the manager has for the next gameweek,
	// assuming no transfers have been made for it yet.
	Next int
	// Mismatches lists gameweeks where the replay predicted a different hit than
	// FPL reported. Any entry means the rules model is wrong for this manager and
	// Next should not be trusted.
	Mismatches []int
}

// FreeTransfers replays a manager's season. FPL does not publish the balance, so
// it is derived: one free transfer arrives each gameweek, unused ones bank up to
// transfers.MaxBankedFreeTransfers, and each transfer beyond the free ones costs
// transfers.HitCost points. Wildcard and Free Hit weeks use no free transfers.
//
// The first recorded gameweek is unlimited (the initial squad, or the squad a
// late joiner builds), so the count starts from nothing banked after it. Every
// later week is cross-checked against the hit FPL actually applied.
func FreeTransfers(h fpl.History) FreeTransferState {
	weeks := append([]fpl.GameweekHistory(nil), h.Current...) // never reorder the caller's slice
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Event < weeks[j].Event })

	unlimited := map[int]bool{}
	for _, c := range h.Chips {
		if c.Name == "wildcard" || c.Name == "freehit" {
			unlimited[c.Event] = true
		}
	}

	var state FreeTransferState
	left := 0 // free transfers still banked after the previous gameweek
	for i, w := range weeks {
		if i == 0 {
			continue // an unlimited opening gameweek
		}
		available := min(left+1, transfers.MaxBankedFreeTransfers)
		if unlimited[w.Event] {
			left = available // a chip week spares the free transfers
			if w.EventTransfersCost != 0 {
				state.Mismatches = append(state.Mismatches, w.Event)
			}
			continue
		}
		if want := transfers.HitCost * max(0, w.EventTransfers-available); want != w.EventTransfersCost {
			state.Mismatches = append(state.Mismatches, w.Event)
		}
		left = max(available-w.EventTransfers, 0)
	}
	state.Next = min(left+1, transfers.MaxBankedFreeTransfers)
	return state
}

// SellPrice is what FPL pays out for a player you own: you keep half of any rise
// since you bought him, rounded down to 0.1m, and absorb a fall in full. Prices
// are in tenths of £m.
func SellPrice(buy, current int) int {
	if current <= buy {
		return current
	}
	return buy + (current-buy)/2
}

// BuyPrices works out what a manager paid for each player in ids. A player
// transferred in costs what that transfer says (the most recent one if he came
// in more than once); anyone else is part of the original squad and costs
// initial[id], his price at the start of the season. An unknown price is an
// error, because a wrong one silently corrupts every affordability check.
func BuyPrices(ids []int, transfers []fpl.Transfer, initial map[int]int) (map[int]int, error) {
	latest := map[int]fpl.Transfer{}
	for _, t := range transfers {
		if cur, ok := latest[t.PlayerIn]; !ok || t.Time.After(cur.Time) {
			latest[t.PlayerIn] = t
		}
	}
	out := make(map[int]int, len(ids))
	for _, id := range ids {
		if t, ok := latest[id]; ok {
			out[id] = t.PlayerInCost
			continue
		}
		price, ok := initial[id]
		if !ok {
			return nil, fmt.Errorf("buy price of player %d is unknown: not in the transfer list and no starting price stored", id)
		}
		out[id] = price
	}
	return out, nil
}

// SellPrices maps each player's id to his selling price today, given what was
// paid for him (see BuyPrices).
func SellPrices(players []fpl.Player, buy map[int]int) map[int]int {
	out := make(map[int]int, len(players))
	for _, p := range players {
		out[p.ID] = SellPrice(buy[p.ID], p.NowCost)
	}
	return out
}

// FinanceClient is satisfied by fpl.HTTPClient.
type FinanceClient interface {
	FetchHistory(ctx context.Context, teamID int) (*fpl.History, error)
	FetchTransfers(ctx context.Context, teamID int) ([]fpl.Transfer, error)
}

// Finances is what a manager has to spend and how many moves they can make free.
type Finances struct {
	Bank int // cash in the bank, tenths of £m
	Free FreeTransferState
	Buy  map[int]int // what was paid for each squad player
	Sell map[int]int // what each squad player would sell for today
}

// LoadFinances works out the manager's free transfers and the selling price of
// every player in team.Squad. startPrices must hold the opening price of every
// squad player who was never transferred in (see db.Store.StartingPrices).
//
// The bank is the one at the last deadline; transfers made since are not visible.
func LoadFinances(ctx context.Context, c FinanceClient, teamID int, team Team, startPrices map[int]int) (Finances, error) {
	history, err := c.FetchHistory(ctx, teamID)
	if err != nil {
		return Finances{}, fmt.Errorf("load finances for team %d: %w", teamID, err)
	}
	moves, err := c.FetchTransfers(ctx, teamID)
	if err != nil {
		return Finances{}, fmt.Errorf("load finances for team %d: %w", teamID, err)
	}

	ids := make([]int, len(team.Squad.Players))
	for i, p := range team.Squad.Players {
		ids[i] = p.ID
	}
	buy, err := BuyPrices(ids, moves, startPrices)
	if err != nil {
		return Finances{}, fmt.Errorf("load finances for team %d: %w", teamID, err)
	}
	return Finances{
		Bank: team.History.Bank,
		Free: FreeTransfers(*history),
		Buy:  buy,
		Sell: SellPrices(team.Squad.Players, buy),
	}, nil
}
