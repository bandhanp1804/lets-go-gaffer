package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// NamedMove is a swap described the way a person says it: by name.
type NamedMove struct {
	Out    string // a player in the squad
	In     string // the player to buy
	InTeam string // optional club (short or full name) to tell two players of one name apart
}

// WhatIfNamedRequest is WhatIfRequest with players given by name.
type WhatIfNamedRequest struct {
	Moves   []NamedMove
	Horizon int
	Free    *int
}

// WhatIfNamed is WhatIf for callers that have names and not ids, such as the assistant.
// Names are matched here, in Go, so a model never guesses which player is meant: a name
// that fits more than one player is an error that lists them, for the caller to resolve.
func (s *Service) WhatIfNamed(ctx context.Context, req WhatIfNamedRequest) (WhatIfView, error) {
	if len(req.Moves) > maxWhatIfMoves {
		return WhatIfView{}, badInput("a plan can have at most %d moves", maxWhatIfMoves)
	}
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return WhatIfView{}, err
	}
	squad := sq.team.Squad.Players
	owned := make(map[int]bool, len(squad))
	for _, p := range squad {
		owned[p.ID] = true
	}

	out := WhatIfRequest{Horizon: req.Horizon, Free: req.Free}
	for _, m := range req.Moves {
		sold, err := resolveOwn(m.Out, squad)
		if err != nil {
			return WhatIfView{}, err
		}
		bought, err := resolveBuy(base, m, sold, owned)
		if err != nil {
			return WhatIfView{}, err
		}
		out.Moves = append(out.Moves, WhatIfMoveRequest{Out: sold.ID, In: bought.ID})
	}
	return s.WhatIf(ctx, out)
}

// fold makes a name comparable: lower case, without accents, and with punctuation read as
// a space, so "Groß" matches "gross" and "Le Fee" matches "E.Le Fée".
func fold(s string) string {
	repl := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "æ", "ae", "ç", "c", "ć", "c", "č", "c", "đ", "d",
		"é", "e", "è", "e", "ê", "e", "ë", "e", "ğ", "g", "í", "i", "ì", "i", "î", "i", "ï", "i", "ı", "i", "ł", "l",
		"ñ", "n", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "ő", "o", "œ", "oe", "š", "s", "ś", "s",
		"ß", "ss", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ű", "u", "ý", "y", "ÿ", "y", "ž", "z", "ź", "z",
		".", " ", "-", " ", "'", "",
	)
	return strings.Join(strings.Fields(repl.Replace(strings.ToLower(s))), " ")
}

// match returns the players whose name is the query, or failing that, contains it.
func match(query string, pool []fpl.Player) []fpl.Player {
	q := fold(query)
	if q == "" {
		return nil
	}
	var exact, partial []fpl.Player
	for _, p := range pool {
		switch name := fold(p.WebName); {
		case name == q:
			exact = append(exact, p)
		case strings.Contains(name, q):
			partial = append(partial, p)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func describe(p fpl.Player, short map[int]string) string {
	return fmt.Sprintf("%s (%s, %s, %s)", p.WebName, short[p.TeamID], p.Position, formatMoney(p.NowCost))
}

func listSome(ps []fpl.Player, short map[int]string) string {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].TotalPoints > ps[j].TotalPoints })
	var parts []string
	for i, p := range ps {
		if i == 6 {
			parts = append(parts, fmt.Sprintf("and %d more", len(ps)-6))
			break
		}
		parts = append(parts, describe(p, short))
	}
	return strings.Join(parts, "; ")
}

// resolveOwn finds the player of the squad the manager means to sell.
func resolveOwn(name string, squad []fpl.Player) (fpl.Player, error) {
	if strings.TrimSpace(name) == "" {
		return fpl.Player{}, badInput("each move needs the name of the player to sell")
	}
	found := match(name, squad)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		names := make([]string, len(squad))
		for i, p := range squad {
			names[i] = p.WebName
		}
		return fpl.Player{}, badInput("none of your players is called %q. Your squad (%d players): %s", name, len(names), strings.Join(names, ", "))
	}
	names := make([]string, len(found))
	for i, p := range found {
		names[i] = p.WebName
	}
	return fpl.Player{}, badInput("%q could mean any of your players %s: use the full name", name, strings.Join(names, ", "))
}

// resolveBuy finds the player to buy. A name that fits several players is narrowed by the
// club if one was given, then by the position of the player being sold (a transfer is like
// for like), then by who is not already in the squad; if it is still ambiguous the error
// lists the candidates.
func resolveBuy(base *baseSnap, m NamedMove, sold fpl.Player, owned map[int]bool) (fpl.Player, error) {
	if strings.TrimSpace(m.In) == "" {
		return fpl.Player{}, badInput("each move needs the name of the player to buy")
	}
	found := match(m.In, base.players)
	if len(found) == 0 {
		return fpl.Player{}, badInput("no player is called %q: use the name as it appears on FPL (for example Salah or Haaland)", m.In)
	}
	narrow := func(keep func(fpl.Player) bool) {
		if len(found) < 2 {
			return
		}
		var kept []fpl.Player
		for _, p := range found {
			if keep(p) {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			found = kept
		}
	}
	if m.InTeam != "" {
		id, err := base.teamID(m.InTeam)
		if err != nil {
			return fpl.Player{}, err
		}
		narrow(func(p fpl.Player) bool { return p.TeamID == id })
	}
	narrow(func(p fpl.Player) bool { return p.Position == sold.Position })
	narrow(func(p fpl.Player) bool { return !owned[p.ID] })
	if len(found) > 1 {
		return fpl.Player{}, badInput("%q could mean %s: say which, by giving his club", m.In, listSome(found, base.short))
	}
	return found[0], nil
}
