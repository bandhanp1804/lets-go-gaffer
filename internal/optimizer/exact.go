package optimizer

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// BenchWeight is how much a bench player's score counts next to a starter's:
// bench players only score when a starter does not play.
const BenchWeight = 0.1

// ErrSearchLimit means the exact search gave up before proving an optimum.
var ErrSearchLimit = errors.New("exact search limit reached")

const maxSearchNodes = 5000

// Candidate is a player a squad could include, priced and scored by the caller.
// Cost is what he costs this particular buyer, which can differ from his market
// price: a player you already own costs what he would sell for.
type Candidate struct {
	Player fpl.Player
	Cost   int // tenths of £m
	Score  float64
}

// Result is the best squad found.
type Result struct {
	Squad fpl.Squad
	Cost  int     // total of the chosen candidates' Cost
	Value float64 // TeamValue of the squad
}

// TeamValue is what a squad is expected to score in a gameweek: its best legal
// starting XI, plus the captain's score again (a captain scores double), plus a
// BenchWeight share of the bench. -Inf if the players cannot field a legal XI.
func TeamValue(players []fpl.Player, score func(fpl.Player) float64) float64 {
	return valueOf(players, score, fplShape)
}

// shape describes a squad: how many of each position, which formations are
// legal, and the club limit. Real FPL is fplShape; tests use a tiny one so the
// search can be checked against complete enumeration.
type shape struct {
	quota      [5]int   // squad size by position, indexed by fpl.Position
	formations [][5]int // starters by position, one entry per legal formation
	maxPerClub int
	noPrune    bool // test hook: skip dominance pruning
}

func (s shape) size() int {
	n := 0
	for _, q := range s.quota {
		n += q
	}
	return n
}

var fplShape = func() shape {
	s := shape{quota: [5]int{0, 2, 5, 5, 3}, maxPerClub: 3}
	for d := 3; d <= 5; d++ {
		for m := 2; m <= 5; m++ {
			if f := 10 - d - m; f >= 1 && f <= 3 {
				s.formations = append(s.formations, [5]int{0, 1, d, m, f})
			}
		}
	}
	return s
}()

// valueOf scores a set of players under a shape's formations. Players are
// grouped by position and ranked; for each formation the top scorers start.
func valueOf(players []fpl.Player, score func(fpl.Player) float64, sh shape) float64 {
	var byPos [5][]float64
	for _, p := range players {
		if p.Position >= fpl.GKP && p.Position <= fpl.FWD {
			byPos[p.Position] = append(byPos[p.Position], score(p))
		}
	}
	var total float64
	for pos := range byPos {
		sort.Sort(sort.Reverse(sort.Float64Slice(byPos[pos])))
		for _, s := range byPos[pos] {
			total += s
		}
	}
	best := math.Inf(-1)
	for _, f := range sh.formations {
		xi, captain, ok := 0.0, math.Inf(-1), true
		for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
			if f[pos] > len(byPos[pos]) {
				ok = false
				break
			}
			for _, s := range byPos[pos][:f[pos]] {
				xi += s
			}
			if f[pos] > 0 {
				captain = math.Max(captain, byPos[pos][0])
			}
		}
		if !ok {
			continue
		}
		if v := xi + captain + BenchWeight*(total-xi); v > best {
			best = v
		}
	}
	return best
}

// BestSquad finds the squad of 15 with the highest TeamValue whose total Cost is
// within budget and which has at most three players from any club. The answer is
// optimal, not a heuristic.
//
// It works in two layers. Ignoring clubs, a dynamic program over each position
// (how many chosen, how much spent) finds the best squad exactly, handling the
// formation and the captain's bonus. If that squad has four from one club, the
// search branches on excluding each of them in turn and takes the best open
// bound next; the first club-legal squad it reaches is therefore the optimum.
// Players that cannot be needed are pruned first (see prune).
func BestSquad(cands []Candidate, budget int) (Result, error) {
	sol, err := solve(cands, budget, fplShape)
	if err != nil {
		return Result{}, err
	}
	scores := make(map[int]float64, len(sol.chosen))
	players := make([]fpl.Player, len(sol.chosen))
	for i, c := range sol.chosen {
		players[i] = c.Player
		scores[c.Player.ID] = c.Score
	}
	squad, _, ok := Lineup(players, func(p fpl.Player) float64 { return scores[p.ID] })
	if !ok {
		return Result{}, fmt.Errorf("%w: the chosen players cannot field a legal XI", ErrInfeasible)
	}
	return Result{Squad: squad, Cost: sol.Cost, Value: sol.Value}, nil
}

type solution struct {
	chosen []Candidate
	Cost   int
	Value  float64
}

// item is a candidate with the fields the search touches most.
type item struct {
	c     Candidate
	id    int
	club  int
	cost  int
	score float64
}

func solve(cands []Candidate, budget int, sh shape) (solution, error) {
	if budget < 0 {
		return solution{}, fmt.Errorf("optimizer: budget cannot be negative, got %d", budget)
	}
	seen := map[int]bool{}
	var byPos [5][]item
	for _, c := range cands {
		id := c.Player.ID
		if seen[id] {
			return solution{}, fmt.Errorf("optimizer: player %d appears twice among the candidates", id)
		}
		seen[id] = true
		pos := c.Player.Position
		if pos < fpl.GKP || pos > fpl.FWD || sh.quota[pos] == 0 || c.Cost > budget {
			continue
		}
		byPos[pos] = append(byPos[pos], item{c: c, id: id, club: c.Player.TeamID, cost: c.Cost, score: c.Score})
	}
	for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
		sort.Slice(byPos[pos], func(i, j int) bool {
			a, b := byPos[pos][i], byPos[pos][j]
			if a.score != b.score {
				return a.score > b.score
			}
			return a.id < b.id
		})
		if len(byPos[pos]) < sh.quota[pos] {
			return solution{}, fmt.Errorf("%w: %d usable %s candidates, need %d", ErrInfeasible, len(byPos[pos]), pos, sh.quota[pos])
		}
		if !sh.noPrune {
			byPos[pos] = prune(byPos[pos], sh, pos)
		}
	}

	// No squad can spend more than its dearest players, so the table need not be wider.
	width := 0
	for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
		costs := make([]int, len(byPos[pos]))
		for i, it := range byPos[pos] {
			costs[i] = it.cost
		}
		sort.Sort(sort.Reverse(sort.IntSlice(costs)))
		for _, c := range costs[:sh.quota[pos]] {
			width += c
		}
	}
	width = min(width, budget)

	s := &searcher{sh: sh, byPos: byPos, width: width, tables: map[string]*table{}}
	return s.run()
}

// prune drops players an optimal squad never needs. Within a position, x is
// redundant if enough better-or-equal, no-dearer players from enough different
// clubs exist that swapping x for one of them is always possible: at most
// quota-1 of them can already be in the squad, and the other players can fill at
// most (size-1)/maxPerClub clubs to the limit, so one outside player from one
// more club than that always fits. The swap keeps the budget and cannot lower
// the value. Dominators are only counted among players already kept, so the
// argument never leans on a player that was itself removed.
func prune(items []item, sh shape, pos fpl.Position) []item {
	need := sh.quota[pos] + (sh.size()-1)/sh.maxPerClub
	var kept []item
	for _, x := range items {
		clubs := map[int]bool{}
		for _, y := range kept { // kept is in score order, so y scores at least as much as x
			if y.cost <= x.cost {
				clubs[y.club] = true
			}
		}
		if len(clubs) >= need {
			continue
		}
		kept = append(kept, x)
	}
	return kept
}

type searcher struct {
	sh     shape
	byPos  [5][]item
	width  int
	tables map[string]*table
}

type node struct {
	forbidden []int // player ids excluded in this branch, ascending
	chosen    []item
	value     float64
}

func (s *searcher) run() (solution, error) {
	root, ok := s.evaluate(nil)
	if !ok {
		return solution{}, fmt.Errorf("%w: no squad fits the budget", ErrInfeasible)
	}
	open := []node{root}
	visited := map[string]bool{key(nil): true}

	for evaluated := 1; len(open) > 0; {
		// Always expand the node with the highest bound: its value is an upper bound on
		// everything beneath it, so the first club-legal node popped is optimal.
		best := 0
		for i := range open {
			if open[i].value > open[best].value {
				best = i
			}
		}
		cur := open[best]
		open = append(open[:best], open[best+1:]...)

		club, members := s.overfull(cur.chosen)
		if club == 0 {
			sol := solution{Value: cur.value}
			for _, it := range cur.chosen {
				sol.chosen = append(sol.chosen, it.c)
				sol.Cost += it.cost
			}
			return sol, nil
		}
		for _, m := range members {
			forbidden := insertSorted(cur.forbidden, m.id)
			k := key(forbidden)
			if visited[k] {
				continue
			}
			visited[k] = true
			if evaluated++; evaluated > maxSearchNodes {
				return solution{}, fmt.Errorf("%w after %d nodes", ErrSearchLimit, maxSearchNodes)
			}
			if child, ok := s.evaluate(forbidden); ok {
				open = append(open, child)
			}
		}
	}
	return solution{}, fmt.Errorf("%w: no squad satisfies the club limit within budget", ErrInfeasible)
}

// overfull returns a club with more players than allowed (the lowest id) and the
// chosen players from it, or 0 if every club is within the limit.
func (s *searcher) overfull(chosen []item) (int, []item) {
	byClub := map[int][]item{}
	for _, it := range chosen {
		byClub[it.club] = append(byClub[it.club], it)
	}
	worst := 0
	for club, members := range byClub {
		if len(members) > s.sh.maxPerClub && (worst == 0 || club < worst) {
			worst = club
		}
	}
	if worst == 0 {
		return 0, nil
	}
	members := byClub[worst]
	sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
	return worst, members
}

// evaluate finds the best squad ignoring clubs, with the given players excluded.
func (s *searcher) evaluate(forbidden []int) (node, bool) {
	best, bestFormation := math.Inf(-1), -1
	var bestChosen []item

	for fi, f := range s.sh.formations {
		var tabs [5]*table
		feasible := true
		for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
			tabs[pos] = s.table(pos, f[pos], forbidden)
			if tabs[pos] == nil {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}
		value, chosen, ok := s.combine(tabs)
		if ok && value > best {
			best, bestFormation, bestChosen = value, fi, chosen
		}
	}
	if bestFormation < 0 {
		return node{}, false
	}
	return node{forbidden: forbidden, chosen: bestChosen, value: best}, true
}

// combine folds the four position tables together over the budget and traces
// back the chosen players. Exactly one position hosts the captain.
func (s *searcher) combine(tabs [5]*table) (float64, []item, bool) {
	B := s.width
	type stage struct {
		val  [2][]float64
		argB [2][]int32
		argC [2][]int8
	}
	negInf := math.Inf(-1)
	newStage := func() stage {
		var st stage
		for c := 0; c < 2; c++ {
			st.val[c] = make([]float64, B+1)
			st.argB[c] = make([]int32, B+1)
			st.argC[c] = make([]int8, B+1)
			for b := range st.val[c] {
				st.val[c][b] = negInf
			}
		}
		return st
	}

	cur := stage{val: tabs[fpl.GKP].val}
	stages := make([]stage, 0, 3)
	for pos := fpl.DEF; pos <= fpl.FWD; pos++ {
		h := tabs[pos].val
		var hb [2][]int
		for c := 0; c < 2; c++ {
			for b, v := range h[c] {
				if v != negInf {
					hb[c] = append(hb[c], b)
				}
			}
		}
		next := newStage()
		for c1 := 0; c1 < 2; c1++ {
			for b1, v1 := range cur.val[c1] {
				if v1 == negInf {
					continue
				}
				for c2 := 0; c1+c2 < 2; c2++ {
					for _, b2 := range hb[c2] {
						b := b1 + b2
						if b > B {
							break
						}
						if v := v1 + h[c2][b2]; v > next.val[c1+c2][b] {
							next.val[c1+c2][b] = v
							next.argB[c1+c2][b] = int32(b1)
							next.argC[c1+c2][b] = int8(c1)
						}
					}
				}
			}
		}
		stages = append(stages, next)
		cur = next
	}

	bestB, best := -1, negInf
	for b, v := range cur.val[1] { // the captain must be somewhere
		if v > best {
			best, bestB = v, b
		}
	}
	if bestB < 0 {
		return 0, nil, false
	}

	var chosen []item
	b, c := bestB, 1
	for i := len(stages) - 1; i >= 0; i-- {
		pos := fpl.DEF + fpl.Position(i)
		b1, c1 := int(stages[i].argB[c][b]), int(stages[i].argC[c][b])
		chosen = append(chosen, tabs[pos].pick(b-b1, c-c1)...)
		b, c = b1, c1
	}
	chosen = append(chosen, tabs[fpl.GKP].pick(b, c)...)
	return best, chosen, true
}

// table is the dynamic program for one position: the best value of choosing the
// position's full quota, as a function of the money spent and whether the
// captain's bonus was given to one of its starters.
type table struct {
	items    []item
	quota    int
	starters int
	width    int
	val      [2][]float64 // [captainHere][cost]
	dec      [][]uint8    // per item, per state: 0 skipped, 1 taken, 2 taken as captain
}

func (t *table) idx(k, b, c int) int { return (k*(t.width+1)+b)*2 + c }

// table builds (or reuses) the table for a position with the given number of
// starters, leaving out forbidden players. It returns nil if the position cannot
// be filled.
func (s *searcher) table(pos fpl.Position, starters int, forbidden []int) *table {
	var skip []int
	for _, id := range forbidden {
		for _, it := range s.byPos[pos] {
			if it.id == id {
				skip = append(skip, id)
			}
		}
	}
	k := fmt.Sprintf("%d/%d/%s", pos, starters, key(skip))
	if t, ok := s.tables[k]; ok {
		return t
	}
	var items []item
	for _, it := range s.byPos[pos] {
		if !slicesContains(skip, it.id) {
			items = append(items, it)
		}
	}
	var t *table
	if len(items) >= s.sh.quota[pos] {
		t = newTable(items, s.sh.quota[pos], starters, s.width)
	}
	s.tables[k] = t
	return t
}

func newTable(items []item, quota, starters, width int) *table {
	t := &table{items: items, quota: quota, starters: starters, width: width}
	size := (quota + 1) * (width + 1) * 2
	negInf := math.Inf(-1)
	prev := make([]float64, size)
	for i := range prev {
		prev[i] = negInf
	}
	prev[t.idx(0, 0, 0)] = 0

	for _, it := range items {
		cur := append([]float64(nil), prev...) // skipping the item is the default
		dec := make([]uint8, size)
		for k := 0; k < quota; k++ {
			weight := BenchWeight
			if k < starters {
				weight = 1 // players are taken best first, so the first `starters` chosen start
			}
			for b := 0; b+it.cost <= width; b++ {
				for c := 0; c < 2; c++ {
					v := prev[t.idx(k, b, c)]
					if v == negInf {
						continue
					}
					nv := v + weight*it.score
					if ti := t.idx(k+1, b+it.cost, c); nv > cur[ti] {
						cur[ti], dec[ti] = nv, 1
					}
					if k < starters && c == 0 { // make this starter the captain
						nv2 := nv + it.score
						if ti := t.idx(k+1, b+it.cost, 1); nv2 > cur[ti] {
							cur[ti], dec[ti] = nv2, 2
						}
					}
				}
			}
		}
		t.dec = append(t.dec, dec)
		prev = cur
	}
	for c := 0; c < 2; c++ {
		t.val[c] = make([]float64, width+1)
		for b := 0; b <= width; b++ {
			t.val[c][b] = prev[t.idx(quota, b, c)]
		}
	}
	return t
}

// pick traces back which players give the table's value for a cost and captain flag.
func (t *table) pick(b, c int) []item {
	var out []item
	k := t.quota
	for i := len(t.items) - 1; i >= 0 && k > 0; i-- {
		switch t.dec[i][t.idx(k, b, c)] {
		case 1:
			out = append(out, t.items[i])
			k--
			b -= t.items[i].cost
		case 2:
			out = append(out, t.items[i])
			k--
			b -= t.items[i].cost
			c = 0
		}
	}
	return out
}

func key(ids []int) string {
	var sb strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&sb, "%d,", id)
	}
	return sb.String()
}

func insertSorted(ids []int, id int) []int {
	out := append(append([]int(nil), ids...), id)
	sort.Ints(out)
	return out
}

func slicesContains(ids []int, id int) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// LegalFormations lists every legal outfield split as {defenders, midfielders,
// forwards}, each totalling ten with the goalkeeper making eleven.
func LegalFormations() [][3]int {
	out := make([][3]int, len(fplShape.formations))
	for i, f := range fplShape.formations {
		out[i] = [3]int{f[fpl.DEF], f[fpl.MID], f[fpl.FWD]}
	}
	return out
}
