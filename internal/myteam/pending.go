package myteam

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

// FPL publishes a squad only as of the last deadline; transfers made since are
// private to the logged-in manager. This file lets the manager state those
// transfers so the tools can work from the squad they actually have.

// SwapSpec is one pending transfer as typed: the names of the player sold and the
// player bought.
type SwapSpec struct {
	Out, In string
}

// Change is a resolved transfer.
type Change struct {
	Out, In fpl.Player
}

// ParseSwaps reads transfers written as "Out>In". Each value may hold several,
// separated by commas.
func ParseSwaps(values []string) ([]SwapSpec, error) {
	var out []SwapSpec
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" && strings.TrimSpace(v) != "" {
				continue // a trailing comma
			}
			sold, bought, ok := strings.Cut(part, ">")
			sold, bought = strings.TrimSpace(sold), strings.TrimSpace(bought)
			if !ok || sold == "" || bought == "" {
				return nil, fmt.Errorf("swap %q must look like Out>In, for example Havertz>Calvert-Lewin", strings.TrimSpace(v))
			}
			out = append(out, SwapSpec{Out: sold, In: bought})
		}
	}
	return out, nil
}

// ResolveSwaps turns names into players. A name matches a player's web name, last
// name, or full name, ignoring case, or may be a numeric player id. The player
// sold must be in the squad; the player bought must be in market and not already
// owned. A name matching several players is refused with the options listed,
// rather than guessed.
func ResolveSwaps(squad, market []fpl.Player, specs []SwapSpec) ([]Change, error) {
	owned := make(map[int]bool, len(squad))
	for _, p := range squad {
		owned[p.ID] = true
	}
	soldIDs, boughtIDs := map[int]bool{}, map[int]bool{}

	var out []Change
	for _, s := range specs {
		sold, err := lookup(squad, s.Out)
		if err != nil {
			if errors.Is(err, errNoMatch) {
				return nil, fmt.Errorf("%q is not in your squad", s.Out)
			}
			return nil, err
		}
		bought, err := lookup(market, s.In)
		if err != nil {
			if errors.Is(err, errNoMatch) {
				return nil, fmt.Errorf("no player named %q", s.In)
			}
			return nil, err
		}
		if owned[bought.ID] {
			return nil, fmt.Errorf("%s is already in your squad", bought.WebName)
		}
		if soldIDs[sold.ID] {
			return nil, fmt.Errorf("%s is sold twice", sold.WebName)
		}
		if boughtIDs[bought.ID] {
			return nil, fmt.Errorf("%s is bought twice", bought.WebName)
		}
		soldIDs[sold.ID], boughtIDs[bought.ID] = true, true
		out = append(out, Change{Out: sold, In: bought})
	}
	return out, nil
}

var errNoMatch = errors.New("no match")

func lookup(players []fpl.Player, name string) (fpl.Player, error) {
	name = strings.TrimSpace(name)
	if id, err := strconv.Atoi(name); err == nil {
		for _, p := range players {
			if p.ID == id {
				return p, nil
			}
		}
		return fpl.Player{}, errNoMatch
	}
	var hits []fpl.Player
	for _, p := range players {
		full := p.FirstName + " " + p.LastName
		if strings.EqualFold(p.WebName, name) || strings.EqualFold(p.LastName, name) || strings.EqualFold(full, name) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return fpl.Player{}, errNoMatch
	case 1:
		return hits[0], nil
	}
	opts := make([]string, len(hits))
	for i, p := range hits {
		opts[i] = fmt.Sprintf("%s (id %d, %s)", p.WebName, p.ID, p.Position)
	}
	return fpl.Player{}, fmt.Errorf("%q matches %d players: %s; use the id", name, len(hits), strings.Join(opts, ", "))
}

// Apply returns the team after the changes: each player bought takes the place of
// the player sold, in the XI or on the bench, and inherits the armband if the
// player sold held it (FPL clears it; assuming it keeps the squad valid, and the
// lineup advisor re-picks the captain anyway). The team given is not modified.
//
// Only the squad's shape is checked: the 2/5/5/3 split, no duplicates, and at
// most three from a club. The XI can be left illegal by a swap across positions,
// which is fine because the lineup advisor chooses a fresh one.
func Apply(t Team, changes []Change) (Team, error) {
	next := t
	next.Squad.Players = append([]fpl.Player(nil), t.Squad.Players...)
	next.Squad.Starting = append([]int(nil), t.Squad.Starting...)

	for _, c := range changes {
		slot := -1
		for i, p := range next.Squad.Players {
			if p.ID == c.Out.ID {
				slot = i
			}
		}
		if slot < 0 {
			return Team{}, fmt.Errorf("%s is not in the squad", c.Out.WebName)
		}
		next.Squad.Players[slot] = c.In
		for i, id := range next.Squad.Starting {
			if id == c.Out.ID {
				next.Squad.Starting[i] = c.In.ID
			}
		}
		if next.Squad.Captain == c.Out.ID {
			next.Squad.Captain = c.In.ID
		}
		if next.Squad.ViceCaptain == c.Out.ID {
			next.Squad.ViceCaptain = c.In.ID
		}
	}

	if err := optimizer.ValidateWithBudget(next.Squad, optimizer.NoBudgetLimit); err != nil {
		var ve *optimizer.ValidationError
		if errors.As(err, &ve) {
			var structural []optimizer.Violation
			for _, v := range ve.Violations {
				switch v.Rule {
				case optimizer.RuleSquadSize, optimizer.RulePositionCounts, optimizer.RuleClubLimit, optimizer.RuleDuplicate:
					structural = append(structural, v)
				}
			}
			if len(structural) > 0 {
				return Team{}, fmt.Errorf("these changes leave an illegal squad: %w", &optimizer.ValidationError{Violations: structural})
			}
		}
	}
	return next, nil
}

// ApplyFinances returns the finances after the changes: the bank gains what the
// players sold fetch and pays for those bought, and a player bought today sells
// for what he cost. Each change uses up a free transfer; beyond the free ones,
// each costs transfers.HitCost points, which is recorded as already incurred. The
// finances given are not modified.
func ApplyFinances(f Finances, changes []Change) Finances {
	next := f
	next.Buy = make(map[int]int, len(f.Buy)+len(changes))
	next.Sell = make(map[int]int, len(f.Sell)+len(changes))
	for k, v := range f.Buy {
		next.Buy[k] = v
	}
	for k, v := range f.Sell {
		next.Sell[k] = v
	}

	for _, c := range changes {
		sale, ok := f.Sell[c.Out.ID]
		if !ok {
			sale = c.Out.NowCost
		}
		next.Bank += sale - c.In.NowCost
		delete(next.Buy, c.Out.ID)
		delete(next.Sell, c.Out.ID)
		next.Buy[c.In.ID] = c.In.NowCost
		next.Sell[c.In.ID] = c.In.NowCost
	}

	free := f.Free.Next
	next.PendingTransfers = f.PendingTransfers + len(changes)
	next.PendingHit = f.PendingHit + transfers.HitCost*max(0, len(changes)-free)
	next.Free.Next = max(free-len(changes), 0)
	return next
}

// DescribeChanges renders changes for a header line: "Havertz → Calvert-Lewin, Cash → Vuskovic".
func DescribeChanges(changes []Change) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = c.Out.WebName + " → " + c.In.WebName
	}
	return strings.Join(parts, ", ")
}
