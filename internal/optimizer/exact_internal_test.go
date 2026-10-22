package optimizer

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// tinyShape is a squad small enough to enumerate completely: 1 GKP, 2 DEF, 2 MID,
// 1 FWD, at most 2 from a club, and two formations.
var tinyShape = shape{
	quota:      [5]int{0, 1, 2, 2, 1},
	formations: [][5]int{{0, 1, 1, 2, 1}, {0, 1, 2, 1, 1}},
	maxPerClub: 2,
}

// combos calls f with every way to choose k items from n.
func combos(n, k int, f func(idx []int)) {
	idx := make([]int, k)
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == k {
			f(idx)
			return
		}
		for i := start; i < n; i++ {
			idx[depth] = i
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
}

// bruteForce tries every squad of the shape and returns the best value, or -Inf.
func bruteForce(cands []Candidate, budget int, sh shape) float64 {
	byPos := map[fpl.Position][]Candidate{}
	for _, c := range cands {
		byPos[c.Player.Position] = append(byPos[c.Player.Position], c)
	}
	best := math.Inf(-1)
	var chosen []Candidate
	var rec func(pos fpl.Position)
	rec = func(pos fpl.Position) {
		if pos > fpl.FWD {
			cost, clubs := 0, map[int]int{}
			players := make([]fpl.Player, len(chosen))
			scores := map[int]float64{}
			for i, c := range chosen {
				cost += c.Cost
				clubs[c.Player.TeamID]++
				players[i] = c.Player
				scores[c.Player.ID] = c.Score
			}
			if cost > budget {
				return
			}
			for _, n := range clubs {
				if n > sh.maxPerClub {
					return
				}
			}
			if v := valueOf(players, func(p fpl.Player) float64 { return scores[p.ID] }, sh); v > best {
				best = v
			}
			return
		}
		pool := byPos[pos]
		combos(len(pool), sh.quota[pos], func(idx []int) {
			n := len(chosen)
			for _, i := range idx {
				chosen = append(chosen, pool[i])
			}
			rec(pos + 1)
			chosen = chosen[:n]
		})
	}
	rec(fpl.GKP)
	return best
}

func randomCandidates(rng *rand.Rand, perPos [5]int, clubs int) []Candidate {
	var out []Candidate
	id := 1
	for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
		for i := 0; i < perPos[pos]; i++ {
			// A few scores repeat, so ties and equal-value alternatives are exercised.
			score := float64(rng.Intn(12))
			out = append(out, Candidate{
				Player: fpl.Player{ID: id, TeamID: 1 + rng.Intn(clubs), Position: pos, NowCost: 40 + rng.Intn(60)},
				Cost:   40 + rng.Intn(60),
				Score:  score,
			})
			id++
		}
	}
	return out
}

func TestSolve_MatchesBruteForce(t *testing.T) {
	solved := 0
	for seed := int64(0); seed < 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		// Many clubs and enough candidates that dominance pruning has work to do.
		cands := randomCandidates(rng, [5]int{0, 4, 7, 7, 4}, 2+rng.Intn(9))
		budget := 240 + rng.Intn(420) // from tight to roomy

		want := bruteForce(cands, budget, tinyShape)
		got, err := solve(cands, budget, tinyShape)

		if math.IsInf(want, -1) {
			if err == nil {
				t.Fatalf("seed %d: brute force found no squad but solve returned one worth %v", seed, got.Value)
			}
			continue
		}
		if err != nil {
			t.Fatalf("seed %d: solve failed (%v) but brute force found a squad worth %v", seed, err, want)
		}
		if math.Abs(got.Value-want) > 1e-9 {
			t.Fatalf("seed %d: solve = %v, brute force = %v", seed, got.Value, want)
		}
		solved++
	}
	if solved < 80 {
		t.Errorf("only %d of 120 instances were feasible; the comparison barely ran", solved)
	}
}

func TestSolve_PruningNeverChangesTheAnswer(t *testing.T) {
	// Pruning is only an optimisation. Switching it off must give the same value.
	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(1000 + seed))
		cands := randomCandidates(rng, [5]int{0, 6, 10, 10, 6}, 3+rng.Intn(10))
		budget := 240 + rng.Intn(420)

		with, errWith := solve(cands, budget, tinyShape)
		off := tinyShape
		off.noPrune = true
		without, errWithout := solve(cands, budget, off)

		if (errWith == nil) != (errWithout == nil) {
			t.Fatalf("seed %d: feasibility differs with pruning: %v vs %v", seed, errWith, errWithout)
		}
		if errWith == nil && math.Abs(with.Value-without.Value) > 1e-9 {
			t.Fatalf("seed %d: pruning changed the optimum from %v to %v", seed, without.Value, with.Value)
		}
	}
}

func TestPrune_ActuallyRemovesPlayersSoThePruningTestsMeanSomething(t *testing.T) {
	pruned, instances := 0, 0
	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(1000 + seed))
		cands := randomCandidates(rng, [5]int{0, 6, 10, 10, 6}, 3+rng.Intn(10))
		var items []item
		for _, c := range cands {
			if c.Player.Position == fpl.MID {
				items = append(items, item{c: c, id: c.Player.ID, club: c.Player.TeamID, cost: c.Cost, score: c.Score})
			}
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].score != items[j].score {
				return items[i].score > items[j].score
			}
			return items[i].id < items[j].id
		})
		instances++
		if len(prune(items, tinyShape, fpl.MID)) < len(items) {
			pruned++
		}
	}
	if pruned < 15 {
		t.Errorf("pruning removed players in only %d of %d instances; the equivalence test is not exercising it", pruned, instances)
	}
}
