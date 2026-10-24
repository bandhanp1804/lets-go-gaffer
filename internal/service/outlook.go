package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

const (
	defaultOutlook    = 4
	maxOutlookWeeks   = 8
	maxOutlookPlayers = 6
	// defaultCandidates is how many of the manager's own players are looked at when none are named:
	// the captain and the best of the rest.
	defaultCandidates = 4
)

// OutlookRequest asks how some players' next few gameweeks look.
type OutlookRequest struct {
	Players   []string // names; empty means the manager's captain and best captaincy options
	Gameweeks int      // how many gameweeks ahead; default 4, at most 8
}

// OutlookWeekView is one gameweek of a player's outlook.
type OutlookWeekView struct {
	Gameweek   int      `json:"gameweek"`
	Fixtures   []string `json:"fixtures"`           // "BHA (H)"; two for a double gameweek, none for a blank
	Difficulty []int    `json:"fixture_difficulty"` // FPL's 1 (easiest) to 5, one per fixture
	XP         float64  `json:"xp"`                 // expected points that gameweek, including the chance of playing

	AsCaptain          float64 `json:"as_captain"`           // xp counted twice
	AsTripleCaptain    float64 `json:"as_triple_captain"`    // xp counted three times
	TripleCaptainExtra float64 `json:"triple_captain_extra"` // what Triple Captain adds over an ordinary captaincy
	BestOther          string  `json:"best_other_in_squad"`  // the manager's best other player that gameweek
	BestOtherXP        float64 `json:"best_other_in_squad_xp"`
	LeadOverSquad      float64 `json:"lead_over_squad"` // xp minus the best other player's; negative when someone else is better
}

// OutlookPlayerView is one player's outlook.
type OutlookPlayerView struct {
	Name     string            `json:"name"`
	Team     string            `json:"team"`
	Position string            `json:"position"`
	Price    string            `json:"price"`
	Status   string            `json:"status"`
	News     string            `json:"news,omitempty"`
	Owned    bool              `json:"in_your_squad"`
	Weeks    []OutlookWeekView `json:"weeks"`
	// BestGameweek is the week with the highest expected points; BestTied lists any other week within
	// a toss-up of it, so a close call is not presented as a clear one.
	BestGameweek int     `json:"best_gameweek"`
	BestXP       float64 `json:"best_gameweek_xp"`
	BestTied     []int   `json:"other_gameweeks_within_a_toss_up"`
	TotalXP      float64 `json:"total_xp"`
}

// OutlookView is the look ahead.
type OutlookView struct {
	FirstGameweek int                 `json:"first_gameweek"`
	Gameweeks     int                 `json:"gameweeks"`
	Players       []OutlookPlayerView `json:"players"`
	Notes         []string            `json:"notes"`
}

// Outlook reads the fixture list ahead and the model's expected points for each upcoming
// gameweek, for the chosen players. It is how to time a chip: Triple Captain belongs in the
// week where the player's expected points are highest and he is clear of the rest of the squad.
func (s *Service) Outlook(ctx context.Context, req OutlookRequest) (OutlookView, error) {
	weeks := req.Gameweeks
	switch {
	case weeks < 0 || weeks > maxOutlookWeeks:
		return OutlookView{}, badInput("gameweeks must be between 1 and %d", maxOutlookWeeks)
	case weeks == 0:
		weeks = defaultOutlook
	}
	if len(req.Players) > maxOutlookPlayers {
		return OutlookView{}, badInput("ask about at most %d players at a time", maxOutlookPlayers)
	}
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return OutlookView{}, err
	}

	// The manager's squad, when there is one: it is what a player is compared with.
	var squad []fpl.Player
	var captain int
	var notes []string
	if _, sq, err := s.squadSnapshot(ctx); err == nil {
		squad, captain, notes = sq.team.Squad.Players, sq.team.Squad.Captain, joinNotes(base.notes, sq.notes)
	} else {
		notes = joinNotes(base.notes)
	}
	owned := make(map[int]bool, len(squad))
	for _, p := range squad {
		owned[p.ID] = true
	}

	last := base.next + weeks - 1
	if last > lastGameweek {
		last = lastGameweek
		notes = append(notes, fmt.Sprintf("The season ends at GW%d, so the look ahead stops there.", lastGameweek))
	}
	xpIn := func(p fpl.Player, gw int) float64 { return base.model.ScoreFrom(gw, 1)(p) }

	var chosen []fpl.Player
	if len(req.Players) == 0 {
		if len(squad) == 0 {
			return OutlookView{}, badInput("name at least one player, for example Haaland")
		}
		chosen = defaultCandidatesOf(squad, captain, base.model.ScoreFrom(base.next, weeks))
	} else {
		seen := map[int]bool{}
		for _, name := range req.Players {
			p, err := resolveAny(base, name, owned)
			if err != nil {
				return OutlookView{}, err
			}
			if !seen[p.ID] {
				seen[p.ID] = true
				chosen = append(chosen, p)
			}
		}
	}

	v := OutlookView{FirstGameweek: base.next, Gameweeks: last - base.next + 1, Players: []OutlookPlayerView{}, Notes: notes}
	for _, p := range chosen {
		pv := OutlookPlayerView{
			Name: p.WebName, Team: base.short[p.TeamID], Position: positionName(p.Position), Price: formatMoney(p.NowCost),
			Status: statusText(p), News: p.News, Owned: owned[p.ID], Weeks: []OutlookWeekView{}, BestTied: []int{},
		}
		if !optimizer.Available(p) {
			v.Notes = append(v.Notes, fmt.Sprintf("%s is %s: his expected points are scaled by his chance of playing, the same in every week, so a return from injury is not modelled.", p.WebName, pv.Status))
		}
		for gw := base.next; gw <= last; gw++ {
			opps := base.fixtures.Opponents(p.TeamID, gw)
			w := OutlookWeekView{Gameweek: gw, Fixtures: []string{}, Difficulty: []int{}, XP: round1(xpIn(p, gw))}
			for _, o := range opps {
				w.Fixtures = append(w.Fixtures, fixtureText([]fpl.Opponent{o}, base.short))
				w.Difficulty = append(w.Difficulty, o.Difficulty)
			}
			// Derived from the rounded figure, so that what is shown multiplies out exactly.
			w.AsCaptain, w.AsTripleCaptain, w.TripleCaptainExtra = round1(2*w.XP), round1(3*w.XP), w.XP
			var bestOther fpl.Player
			bestOtherXP := -1.0
			for _, q := range squad {
				if q.ID == p.ID {
					continue
				}
				if x := round1(xpIn(q, gw)); x > bestOtherXP {
					bestOther, bestOtherXP = q, x
				}
			}
			if bestOtherXP >= 0 {
				w.BestOther, w.BestOtherXP, w.LeadOverSquad = bestOther.WebName, bestOtherXP, round1(w.XP-bestOtherXP)
			}
			pv.Weeks = append(pv.Weeks, w)
			pv.TotalXP = round1(pv.TotalXP + w.XP)
		}
		best := 0
		for i, w := range pv.Weeks {
			if w.XP > pv.Weeks[best].XP {
				best = i
			}
		}
		if len(pv.Weeks) > 0 {
			pv.BestGameweek, pv.BestXP = pv.Weeks[best].Gameweek, pv.Weeks[best].XP
			for i, w := range pv.Weeks {
				if i != best && pv.BestXP-w.XP <= tossUp {
					pv.BestTied = append(pv.BestTied, w.Gameweek)
				}
			}
		}
		v.Players = append(v.Players, pv)
	}
	return v, nil
}

// defaultCandidatesOf is the captain and the best of the rest of the squad, by expected points
// over the horizon: who a chip such as Triple Captain would be played on.
func defaultCandidatesOf(squad []fpl.Player, captain int, score func(fpl.Player) float64) []fpl.Player {
	ranked := append([]fpl.Player(nil), squad...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if si, sj := score(ranked[i]), score(ranked[j]); si != sj {
			return si > sj
		}
		return ranked[i].ID < ranked[j].ID
	})
	var out []fpl.Player
	for _, p := range squad {
		if p.ID == captain {
			out = append(out, p)
		}
	}
	for _, p := range ranked {
		if len(out) >= defaultCandidates+1 {
			break
		}
		if p.ID != captain {
			out = append(out, p)
		}
	}
	return out
}

// resolveAny finds a player by name among everyone. A name two players share is narrowed to the
// manager's own, then to the one with more points; if it is still ambiguous the error lists them.
func resolveAny(base *baseSnap, name string, owned map[int]bool) (fpl.Player, error) {
	if strings.TrimSpace(name) == "" {
		return fpl.Player{}, badInput("each player needs a name")
	}
	found := match(name, base.players)
	if len(found) == 0 {
		return fpl.Player{}, badInput("no player is called %q: use the name as it appears on FPL (for example Salah or Haaland)", name)
	}
	if len(found) > 1 {
		var mine []fpl.Player
		for _, p := range found {
			if owned[p.ID] {
				mine = append(mine, p)
			}
		}
		if len(mine) > 0 {
			found = mine
		}
	}
	if len(found) > 1 {
		return fpl.Player{}, badInput("%q could mean %s: use the full name or add his club", name, listSome(found, base.short))
	}
	return found[0], nil
}
