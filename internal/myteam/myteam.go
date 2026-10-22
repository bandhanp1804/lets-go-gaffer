// Package myteam turns a manager's public FPL picks into a fpl.Squad.
package myteam

import (
	"context"
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

const squadSize = 15

// Client is satisfied by fpl.HTTPClient.
type Client interface {
	FetchEntry(ctx context.Context, teamID int) (*fpl.Entry, error)
	FetchPicks(ctx context.Context, teamID, gameweek int) (*fpl.Picks, error)
}

// Team is a manager's squad as of their latest visible gameweek deadline.
type Team struct {
	Entry   fpl.Entry
	Event   int // the gameweek the squad is from
	Squad   fpl.Squad
	History fpl.EntryHistory // points, rank, bank and value (tenths of £m) for that gameweek
	Chip    string           // chip played that gameweek, or ""
}

// Load fetches a manager's squad for their current gameweek and joins each pick
// with the stored player, so prices, form, and availability are ours.
//
// This is the squad at the last deadline. Transfers made since need a logged-in
// session and are not visible, so it can lag the manager's pending team.
func Load(ctx context.Context, c Client, teamID int, players []fpl.Player) (Team, error) {
	entry, err := c.FetchEntry(ctx, teamID)
	if err != nil {
		return Team{}, fmt.Errorf("load team %d: %w", teamID, err)
	}
	if entry.CurrentEvent < 1 {
		return Team{}, fmt.Errorf("load team %d: no gameweek has been played yet", teamID)
	}
	picks, err := c.FetchPicks(ctx, teamID, entry.CurrentEvent)
	if err != nil {
		return Team{}, fmt.Errorf("load team %d gameweek %d: %w", teamID, entry.CurrentEvent, err)
	}
	if len(picks.Picks) != squadSize {
		return Team{}, fmt.Errorf("load team %d gameweek %d: have %d picks, want %d",
			teamID, entry.CurrentEvent, len(picks.Picks), squadSize)
	}

	byID := make(map[int]fpl.Player, len(players))
	for _, p := range players {
		byID[p.ID] = p
	}

	ordered := append([]fpl.Pick(nil), picks.Picks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })

	squad := fpl.Squad{}
	for i, pick := range ordered {
		p, ok := byID[pick.Element]
		if !ok {
			return Team{}, fmt.Errorf("load team %d: player %d is not in the database; run `gaffer sync` to refresh players",
				teamID, pick.Element)
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
	}
	if squad.Captain == 0 {
		return Team{}, fmt.Errorf("load team %d gameweek %d: picks have no captain", teamID, entry.CurrentEvent)
	}

	team := Team{Entry: *entry, Event: entry.CurrentEvent, Squad: squad, History: picks.EntryHistory}
	if picks.ActiveChip != nil {
		team.Chip = *picks.ActiveChip
	}
	return team, nil
}
