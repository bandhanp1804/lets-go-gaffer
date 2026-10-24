package myteam

import (
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// WithLiveSquad replaces a team's squad with the one FPL reports now for the
// logged-in manager, which includes transfers made since the last deadline, and
// returns the matching finances. Unlike LoadFinances, nothing here is modelled:
// the bank, the selling and buying prices, and the transfers made are FPL's own.
//
// The manager's name, gameweek, and last result carry over from team, which comes
// from the public data. The team given is not modified.
func WithLiveSquad(team Team, live *fpl.MyTeam, players []fpl.Player) (Team, Finances, error) {
	if len(live.Picks) != squadSize {
		return Team{}, Finances{}, fmt.Errorf("live squad has %d picks, want %d", len(live.Picks), squadSize)
	}
	byID := make(map[int]fpl.Player, len(players))
	for _, p := range players {
		byID[p.ID] = p
	}

	picks := append([]fpl.MyPick(nil), live.Picks...)
	sort.Slice(picks, func(i, j int) bool { return picks[i].Position < picks[j].Position })

	squad := fpl.Squad{}
	fin := Finances{
		Bank: live.Transfers.Bank,
		// FPL reports the allowance and how many were made; what is left to use is the difference.
		Free:             FreeTransferState{Next: max(live.Transfers.Limit-live.Transfers.Made, 0)},
		Buy:              make(map[int]int, squadSize),
		Sell:             make(map[int]int, squadSize),
		PendingTransfers: live.Transfers.Made,
		PendingHit:       pendingHit(live.Transfers),
	}
	for i, pick := range picks {
		p, ok := byID[pick.Element]
		if !ok {
			return Team{}, Finances{}, fmt.Errorf("player %d is in your live squad but not in the database; run `gaffer sync` to refresh players", pick.Element)
		}
		squad.Players = append(squad.Players, p)
		if i < 11 {
			squad.Starting = append(squad.Starting, p.ID)
		}
		if pick.IsCaptain {
			squad.Captain = p.ID
		}
		if pick.IsViceCaptain {
			squad.ViceCaptain = p.ID
		}
		fin.Buy[p.ID] = pick.PurchasePrice
		fin.Sell[p.ID] = pick.SellingPrice
	}
	if squad.Captain == 0 {
		return Team{}, Finances{}, fmt.Errorf("live squad has no captain")
	}
	// FPL's own squad must satisfy the rules; if not, our rule model is wrong and the
	// numbers built on it cannot be trusted.
	if err := optimizer.ValidateWithBudget(squad, optimizer.NoBudgetLimit); err != nil {
		return Team{}, Finances{}, fmt.Errorf("your live squad fails the rules check, which means a rule is modelled wrongly: %w", err)
	}
	for _, c := range live.Chips {
		if c.Status == "available" {
			fin.AvailableChips = append(fin.AvailableChips, c.Name)
		}
	}

	next := team
	next.Squad = squad
	// Show the live position in the header, not the stale one from the last deadline,
	// and drop last gameweek's transfer count, which this week's position replaces.
	next.History.Bank = live.Transfers.Bank
	next.History.Value = live.Transfers.Value
	next.History.EventTransfers, next.History.EventTransfersCost = 0, 0
	return next, fin, nil
}

// pendingHit is the points already lost this gameweek. FPL's transfers.cost is the price of
// each transfer beyond the free ones, so it reads 4 even when nothing has been made; the hit
// is that price times the transfers over the allowance. A limit of zero means no fixed
// allowance (a Wildcard or Free Hit is active), where nothing is ever charged.
func pendingHit(t fpl.MyTransfers) int {
	if t.Limit <= 0 {
		return 0
	}
	return max(t.Made-t.Limit, 0) * t.Cost
}
