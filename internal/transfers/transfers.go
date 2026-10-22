// Package transfers finds the best player swaps for an existing FPL squad.
//
// It is plain Go with no I/O. It accounts for the rules that make transfers
// different from building a squad from scratch: each gameweek brings a limited
// number of free transfers, every extra transfer costs points, and the money
// available is what the outgoing players sell for, not their market price.
package transfers

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

const (
	// HitCost is the points deducted for each transfer beyond the free ones.
	HitCost = 4
	// MaxBankedFreeTransfers is how many unused free transfers can be saved
	// (FPL 2026/27: five; it was two in earlier seasons).
	MaxBankedFreeTransfers = 5
	// BenchWeight is how much a bench player's score counts next to a starter's:
	// bench players only score when a starter does not play.
	BenchWeight = 0.1

	defaultMaxTransfers = 2
	defaultTopN         = 5
	defaultCandidates   = 30
	squadSize           = 15
	maxPerClub          = 3
	maxWildcardRounds   = 30
	epsilon             = 1e-9
)

// ErrInvalidSquad means the squad handed in is not a legal FPL squad to start from.
var ErrInvalidSquad = errors.New("invalid squad")

// squadShape is the required number of players per position.
var squadShape = map[fpl.Position]int{fpl.GKP: 2, fpl.DEF: 5, fpl.MID: 5, fpl.FWD: 3}

// Input is the manager's situation. Money is in tenths of £m.
type Input struct {
	Squad []fpl.Player // the 15 players owned now
	Sell  map[int]int  // player id -> what he would sell for today (see myteam.SellPrice)
	Bank  int          // cash in the bank
	Pool  []fpl.Player // everyone who could be bought; owned players in it are ignored
}

// Options tunes the search. Horizon is required; the rest have defaults.
type Options struct {
	Score    func(fpl.Player) float64 // nil uses optimizer.FormScore
	Eligible func(fpl.Player) bool    // nil uses optimizer.Available

	// FreeTransfers is how many transfers cost nothing this gameweek. Each one
	// beyond it costs HitCost points.
	FreeTransfers int
	// MaxTransfers caps how many players change; default 2.
	MaxTransfers int
	// Horizon is how many gameweeks a gain in score is assumed to last. It turns
	// a per-gameweek gain into points that can be weighed against a hit.
	Horizon int
	// TopN is how many suggestions to return; default 5.
	TopN int
	// Candidates bounds the search to this many promising buys per position (by
	// score, and by score per price); default 30.
	Candidates int
}

// Move swaps one player for another of the same position.
type Move struct {
	Out       fpl.Player
	In        fpl.Player
	SellPrice int // what Out sells for
}

// Suggestion is a set of moves and what they are worth.
type Suggestion struct {
	Moves      []Move
	Transfers  int
	Hit        int     // points lost: HitCost for each transfer beyond the free ones
	CostChange int     // money spent on balance (negative frees money): bought minus sold
	BankAfter  int     // cash left
	ScoreGain  float64 // change in TeamScore, per gameweek
	Net        float64 // Horizon x ScoreGain - Hit
	Squad      fpl.Squad
}

// TeamScore is how much a squad is expected to score in a gameweek: its best
// starting XI with the captain counted twice, plus a small share of the bench.
func TeamScore(players []fpl.Player, score func(fpl.Player) float64) float64 {
	squad, total, ok := optimizer.Lineup(players, score)
	if !ok {
		return math.Inf(-1)
	}
	var all, xi float64
	started := map[int]bool{}
	for _, id := range squad.Starting {
		started[id] = true
	}
	for _, p := range players {
		s := score(p)
		all += s
		if started[p.ID] {
			xi += s
		}
	}
	return total + BenchWeight*(all-xi)
}

// Suggest ranks the best transfer plans of up to MaxTransfers moves, best first.
// Only plans that come out ahead after their hits are returned, so an empty
// result means "keep the squad as it is". The result is deterministic: ties go
// to fewer transfers, then to lower player ids.
func Suggest(in Input, o Options) ([]Suggestion, error) {
	s, err := newSearch(in, o)
	if err != nil {
		return nil, err
	}
	base := s.teamScore(in.Squad)

	var out []Suggestion
	s.enumerate(in.Squad, in.Sell, in.Bank, s.maxTransfers, func(c candidate) {
		hit := HitCost * max(0, len(c.outs)-o.FreeTransfers)
		net := float64(o.Horizon)*(c.score-base) - float64(hit)
		if net <= epsilon {
			return
		}
		out = append(out, s.suggestion(in, c, base, hit, net))
	})

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Net != b.Net {
			return a.Net > b.Net
		}
		if a.Transfers != b.Transfers {
			return a.Transfers < b.Transfers
		}
		if c := compareIDs(moveIDs(a, true), moveIDs(b, true)); c != 0 {
			return c < 0
		}
		return compareIDs(moveIDs(a, false), moveIDs(b, false)) < 0
	})
	if len(out) > s.topN {
		out = out[:s.topN]
	}
	return out, nil
}

// Wildcard rebuilds the squad with no limit on transfers and no hit, as a
// Wildcard (or Free Hit) allows. It starts from the current squad and keeps
// applying the best available improvement until none remains, so it never makes
// a change that does not help and never overspends. That is a local search, not
// a proof of optimality.
func Wildcard(in Input, o Options) (Suggestion, error) {
	s, err := newSearch(in, o)
	if err != nil {
		return Suggestion{}, err
	}
	squad := append([]fpl.Player(nil), in.Squad...)
	sell := make(map[int]int, len(in.Sell))
	for k, v := range in.Sell {
		sell[k] = v
	}
	bank := in.Bank
	base := s.teamScore(squad)
	cur := base

	for round := 0; round < maxWildcardRounds; round++ {
		var best *candidate
		s.enumerate(squad, sell, bank, defaultMaxTransfers, func(c candidate) {
			if c.score-cur <= epsilon {
				return
			}
			if best == nil || c.score > best.score+epsilon ||
				(math.Abs(c.score-best.score) <= epsilon && lessCandidate(squad, c, *best)) {
				cp := c
				best = &cp
			}
		})
		if best == nil {
			break
		}
		for k, idx := range best.outs {
			bank += sell[squad[idx].ID]
			in := best.ins[k]
			bank -= in.NowCost
			sell[in.ID] = in.NowCost // bought today at market price: no profit to share yet
			squad[idx] = in
		}
		cur = best.score
	}

	return s.diff(in, squad, bank, base, cur), nil
}

// candidate is one set of simultaneous swaps found by the search.
type candidate struct {
	outs  []int        // indices into the squad being changed
	ins   []fpl.Player // the player bought for each out, in the same order
	score float64      // TeamScore of the resulting squad
}

type search struct {
	score        func(fpl.Player) float64
	maxTransfers int
	topN         int
	cands        map[fpl.Position][]fpl.Player
}

func newSearch(in Input, o Options) (*search, error) {
	switch {
	case o.Horizon < 1:
		return nil, fmt.Errorf("transfers: horizon must be at least 1 gameweek, got %d", o.Horizon)
	case o.FreeTransfers < 0:
		return nil, fmt.Errorf("transfers: free transfers cannot be negative, got %d", o.FreeTransfers)
	case in.Bank < 0:
		return nil, fmt.Errorf("transfers: bank cannot be negative, got %d", in.Bank)
	}
	if err := checkSquad(in); err != nil {
		return nil, err
	}

	s := &search{score: o.Score, maxTransfers: o.MaxTransfers, topN: o.TopN}
	if s.score == nil {
		s.score = optimizer.FormScore
	}
	if s.maxTransfers < 1 {
		s.maxTransfers = defaultMaxTransfers
	}
	if s.topN < 1 {
		s.topN = defaultTopN
	}
	eligible := o.Eligible
	if eligible == nil {
		eligible = optimizer.Available
	}
	limit := o.Candidates
	if limit < 1 {
		limit = defaultCandidates
	}

	owned := make(map[int]bool, squadSize)
	for _, p := range in.Squad {
		owned[p.ID] = true
	}
	byPos := map[fpl.Position][]fpl.Player{}
	for _, p := range in.Pool {
		if !owned[p.ID] && eligible(p) {
			byPos[p.Position] = append(byPos[p.Position], p)
		}
	}
	s.cands = map[fpl.Position][]fpl.Player{}
	for pos, ps := range byPos {
		s.cands[pos] = s.shortlist(ps, limit)
	}
	return s, nil
}

// shortlist keeps the best buys by score and by score per price (the latter finds
// cheap players that free up money), in a stable order.
func (s *search) shortlist(ps []fpl.Player, limit int) []fpl.Player {
	pick := func(less func(a, b fpl.Player) bool) []fpl.Player {
		cp := append([]fpl.Player(nil), ps...)
		sort.Slice(cp, func(i, j int) bool { return less(cp[i], cp[j]) })
		return cp[:min(limit, len(cp))]
	}
	byScore := pick(func(a, b fpl.Player) bool {
		if sa, sb := s.score(a), s.score(b); sa != sb {
			return sa > sb
		}
		return a.ID < b.ID
	})
	byValue := pick(func(a, b fpl.Player) bool {
		va, vb := s.score(a)/float64(max(a.NowCost, 1)), s.score(b)/float64(max(b.NowCost, 1))
		if va != vb {
			return va > vb
		}
		return a.ID < b.ID
	})
	seen := map[int]bool{}
	var out []fpl.Player
	for _, p := range append(byScore, byValue...) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *search) teamScore(players []fpl.Player) float64 { return TeamScore(players, s.score) }

// enumerate calls yield for every affordable, club-legal set of 1..maxK
// same-position swaps. Two players of the same position bought together are
// generated once, not once per order.
func (s *search) enumerate(squad []fpl.Player, sell map[int]int, bank, maxK int, yield func(candidate)) {
	clubs := map[int]int{}
	owned := map[int]bool{}
	for _, p := range squad {
		clubs[p.TeamID]++
		owned[p.ID] = true // a Wildcard round may already own someone the shortlist offers
	}
	trial := make([]fpl.Player, len(squad))

	try := func(outs []int, ins []fpl.Player) {
		money := bank
		for _, i := range outs {
			money += sell[squad[i].ID]
			clubs[squad[i].TeamID]--
		}
		ok := true
		for _, p := range ins {
			money -= p.NowCost
			clubs[p.TeamID]++
		}
		if money < 0 {
			ok = false
		}
		for _, p := range ins {
			if clubs[p.TeamID] > maxPerClub {
				ok = false
			}
		}
		for _, p := range ins {
			clubs[p.TeamID]--
		}
		for _, i := range outs {
			clubs[squad[i].TeamID]++
		}
		if !ok {
			return
		}
		copy(trial, squad)
		for k, i := range outs {
			trial[i] = ins[k]
		}
		yield(candidate{
			outs:  append([]int(nil), outs...),
			ins:   append([]fpl.Player(nil), ins...),
			score: s.teamScore(trial),
		})
	}

	for i := range squad {
		for _, a := range s.cands[squad[i].Position] {
			if owned[a.ID] {
				continue
			}
			try([]int{i}, []fpl.Player{a})
		}
	}
	if maxK < 2 {
		return
	}
	for i := range squad {
		for j := i + 1; j < len(squad); j++ {
			for _, a := range s.cands[squad[i].Position] {
				for _, b := range s.cands[squad[j].Position] {
					if a.ID == b.ID || owned[a.ID] || owned[b.ID] ||
						(squad[i].Position == squad[j].Position && a.ID > b.ID) {
						continue
					}
					try([]int{i, j}, []fpl.Player{a, b})
				}
			}
		}
	}
}

// suggestion turns a candidate into the full report.
func (s *search) suggestion(in Input, c candidate, base float64, hit int, net float64) Suggestion {
	next := append([]fpl.Player(nil), in.Squad...)
	moves := make([]Move, len(c.outs))
	bought, sold := 0, 0
	for k, i := range c.outs {
		moves[k] = Move{Out: in.Squad[i], In: c.ins[k], SellPrice: in.Sell[in.Squad[i].ID]}
		next[i] = c.ins[k]
		bought += c.ins[k].NowCost
		sold += moves[k].SellPrice
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].Out.ID < moves[j].Out.ID })
	lineup, _, _ := optimizer.Lineup(next, s.score)
	return Suggestion{
		Moves: moves, Transfers: len(moves), Hit: hit,
		CostChange: bought - sold, BankAfter: in.Bank - bought + sold,
		ScoreGain: c.score - base, Net: net, Squad: lineup,
	}
}

// diff reports a rebuilt squad as moves from the original: outgoing and incoming
// players of each position are paired in id order.
func (s *search) diff(in Input, final []fpl.Player, bank int, base, score float64) Suggestion {
	kept := map[int]bool{}
	for _, p := range final {
		kept[p.ID] = true
	}
	owned := map[int]bool{}
	for _, p := range in.Squad {
		owned[p.ID] = true
	}
	var moves []Move
	bought, sold := 0, 0
	for _, pos := range []fpl.Position{fpl.GKP, fpl.DEF, fpl.MID, fpl.FWD} {
		var gone, came []fpl.Player
		for _, p := range in.Squad {
			if p.Position == pos && !kept[p.ID] {
				gone = append(gone, p)
			}
		}
		for _, p := range final {
			if p.Position == pos && !owned[p.ID] {
				came = append(came, p)
			}
		}
		sort.Slice(gone, func(i, j int) bool { return gone[i].ID < gone[j].ID })
		sort.Slice(came, func(i, j int) bool { return came[i].ID < came[j].ID })
		for k := range gone {
			moves = append(moves, Move{Out: gone[k], In: came[k], SellPrice: in.Sell[gone[k].ID]})
			bought += came[k].NowCost
			sold += in.Sell[gone[k].ID]
		}
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].Out.ID < moves[j].Out.ID })
	lineup, _, _ := optimizer.Lineup(final, s.score)
	return Suggestion{
		Moves: moves, Transfers: len(moves), Hit: 0,
		CostChange: bought - sold, BankAfter: bank,
		ScoreGain: score - base, Net: score - base, Squad: lineup,
	}
}

// checkSquad confirms the starting squad is legal, so every move built on it is.
func checkSquad(in Input) error {
	if len(in.Squad) != squadSize {
		return fmt.Errorf("%w: have %d players, need %d", ErrInvalidSquad, len(in.Squad), squadSize)
	}
	seen := map[int]bool{}
	shape := map[fpl.Position]int{}
	clubs := map[int]int{}
	for _, p := range in.Squad {
		if seen[p.ID] {
			return fmt.Errorf("%w: player %d appears twice", ErrInvalidSquad, p.ID)
		}
		seen[p.ID] = true
		shape[p.Position]++
		clubs[p.TeamID]++
		if _, ok := in.Sell[p.ID]; !ok {
			return fmt.Errorf("transfers: no selling price for player %d", p.ID)
		}
	}
	for pos, want := range squadShape {
		if shape[pos] != want {
			return fmt.Errorf("%w: have %d %s, need %d", ErrInvalidSquad, shape[pos], pos, want)
		}
	}
	for club, n := range clubs {
		if n > maxPerClub {
			return fmt.Errorf("%w: %d players from club %d, max %d", ErrInvalidSquad, n, club, maxPerClub)
		}
	}
	return nil
}

func moveIDs(s Suggestion, out bool) []int {
	ids := make([]int, len(s.Moves))
	for i, m := range s.Moves {
		if out {
			ids[i] = m.Out.ID
		} else {
			ids[i] = m.In.ID
		}
	}
	return ids
}

func compareIDs(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// lessCandidate breaks score ties between candidates deterministically.
func lessCandidate(squad []fpl.Player, a, b candidate) bool {
	key := func(c candidate) []int {
		ids := make([]int, 0, 2*len(c.outs))
		for _, i := range c.outs {
			ids = append(ids, squad[i].ID)
		}
		for _, p := range c.ins {
			ids = append(ids, p.ID)
		}
		return ids
	}
	return compareIDs(key(a), key(b)) < 0
}
