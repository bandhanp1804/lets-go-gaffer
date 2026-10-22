package optimizer_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// candidatesFrom prices each player at his NowCost and scores him by TotalPoints.
func candidatesFrom(ps []fpl.Player) []optimizer.Candidate {
	out := make([]optimizer.Candidate, len(ps))
	for i, p := range ps {
		out[i] = optimizer.Candidate{Player: p, Cost: p.NowCost, Score: float64(p.TotalPoints)}
	}
	return out
}

func mustBest(t *testing.T, cs []optimizer.Candidate, budget int) optimizer.Result {
	t.Helper()
	r, err := optimizer.BestSquad(cs, budget)
	if err != nil {
		t.Fatalf("BestSquad: %v", err)
	}
	// Costs here are the caller's, not the market's, so the budget is checked on those.
	if err := optimizer.ValidateWithBudget(r.Squad, optimizer.NoBudgetLimit); err != nil {
		t.Fatalf("not a legal squad: %v", err)
	}
	if r.Cost > budget {
		t.Fatalf("cost %d exceeds the budget %d", r.Cost, budget)
	}
	return r
}

func TestBestSquad_PicksTheBestPlayersWhenNothingBinds(t *testing.T) {
	r := mustBest(t, candidatesFrom(ample()), 1000)

	want := []int{1, 2, 11, 12, 13, 14, 15, 21, 22, 23, 24, 25, 31, 32, 33}
	if got := ids(r.Squad.Players); !slices.Equal(got, want) {
		t.Errorf("squad = %v, want the top 2/5/5/3 by score %v", got, want)
	}
}

func TestBestSquad_RespectsTheClubLimit(t *testing.T) {
	pool := ample()
	for i := range pool {
		if pool[i].Position == fpl.MID && pool[i].ID <= 25 {
			pool[i].TeamID = 100 // the five best midfielders share a club
		}
	}

	r := mustBest(t, candidatesFrom(pool), 1000)

	var mids []int
	for _, p := range r.Squad.Players {
		if p.Position == fpl.MID {
			mids = append(mids, p.ID)
		}
	}
	slices.Sort(mids)
	if want := []int{21, 22, 23, 26, 27}; !slices.Equal(mids, want) {
		t.Errorf("midfielders = %v, want three from the shared club then the next best %v", mids, want)
	}
}

func TestBestSquad_SpendsTheBudgetWhenThatBuysMoreScore(t *testing.T) {
	// Goalkeepers: a star at 90 scoring 20, a decent one at 40 scoring 15, a bench filler at 40.
	// Everyone else is a 40 scoring 5. With exactly enough money for the star plus fillers,
	// a squad that skipped the star would leave money unspent.
	var pool []fpl.Player
	id := 1
	add := func(pos fpl.Position, n int, cost, score int) {
		for i := 0; i < n; i++ {
			pool = append(pool, fpl.Player{ID: id, TeamID: id, Position: pos, NowCost: cost, TotalPoints: score, Status: "a"})
			id++
		}
	}
	add(fpl.GKP, 1, 90, 20)
	add(fpl.GKP, 2, 40, 15)
	add(fpl.DEF, 5, 40, 5)
	add(fpl.MID, 5, 40, 5)
	add(fpl.FWD, 3, 40, 5)
	budget := 90 + 14*40 // the star plus fourteen 40s: exactly spent

	r := mustBest(t, candidatesFrom(pool), budget)

	if !slices.Contains(ids(r.Squad.Players), 1) {
		t.Errorf("the 20-point goalkeeper should be bought when the budget exactly allows it: %v", ids(r.Squad.Players))
	}
	if r.Cost != budget {
		t.Errorf("cost = %d, want the whole budget %d to be put to work", r.Cost, budget)
	}
}

func TestBestSquad_UsesTheCostItIsGivenNotTheMarketPrice(t *testing.T) {
	// Owned players cost their (lower) selling price; this must be what counts.
	pool := ample()
	cs := candidatesFrom(pool)
	for i := range cs {
		cs[i].Cost = 10 // everything is nearly free
	}

	r := mustBest(t, cs, 15*10)

	if r.Cost != 150 {
		t.Errorf("cost = %d, want 15 players at the given cost of 10", r.Cost)
	}
}

func TestBestSquad_Infeasible(t *testing.T) {
	noForwards := slices.DeleteFunc(ample(), func(p fpl.Player) bool { return p.Position == fpl.FWD })
	tests := []struct {
		name   string
		cs     []optimizer.Candidate
		budget int
	}{
		{"budget below the cheapest squad", candidatesFrom(ample()), 15*40 - 1},
		{"a position with too few players", candidatesFrom(noForwards), 1000},
		{"no candidates", nil, 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := optimizer.BestSquad(tc.cs, tc.budget); !errors.Is(err, optimizer.ErrInfeasible) {
				t.Errorf("err = %v, want ErrInfeasible", err)
			}
		})
	}
	t.Run("budget exactly the cheapest squad", func(t *testing.T) {
		if r := mustBest(t, candidatesFrom(ample()), 15*40); r.Cost != 15*40 {
			t.Errorf("cost = %d", r.Cost)
		}
	})
}

func TestBestSquad_IsDeterministicWhateverTheInputOrder(t *testing.T) {
	pool := ample()
	for i := range pool {
		pool[i].TotalPoints = 50 // total ties: only the tie-break decides
	}
	first := mustBest(t, candidatesFrom(slices.Clone(pool)), 1000)

	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 10; i++ {
		shuffled := slices.Clone(pool)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got := mustBest(t, candidatesFrom(shuffled), 1000)

		if !slices.Equal(ids(got.Squad.Players), ids(first.Squad.Players)) || got.Value != first.Value {
			t.Fatalf("run %d differs from the first", i)
		}
	}
}

func TestBestSquad_DoesNotMutateItsInput(t *testing.T) {
	cs := candidatesFrom(ample())
	before := slices.Clone(cs)

	mustBest(t, cs, 1000)

	if !slices.Equal(cs, before) {
		t.Error("BestSquad reordered or modified the caller's slice")
	}
}

func TestBestSquad_ValueMatchesTeamValue(t *testing.T) {
	r := mustBest(t, candidatesFrom(ample()), 1000)

	score := func(p fpl.Player) float64 { return float64(p.TotalPoints) }
	if got := optimizer.TeamValue(r.Squad.Players, score); math.Abs(got-r.Value) > 1e-9 {
		t.Errorf("reported value %v, recomputed %v", r.Value, got)
	}
}

func TestTeamValue_AgreesWithTheLineup(t *testing.T) {
	score := func(p fpl.Player) float64 { return float64(p.TotalPoints) }
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		pool := ample()
		for i := range pool {
			pool[i].TotalPoints = rng.Intn(30)
		}
		squad := mustGreedy(t, pool, byPoints)

		lineup, total, ok := optimizer.Lineup(squad.Players, score)
		if !ok {
			t.Fatal("no lineup")
		}
		started := map[int]bool{}
		var xi, all float64
		for _, id := range lineup.Starting {
			started[id] = true
		}
		for _, p := range squad.Players {
			all += score(p)
			if started[p.ID] {
				xi += score(p)
			}
		}
		want := total + optimizer.BenchWeight*(all-xi)

		if got := optimizer.TeamValue(squad.Players, score); math.Abs(got-want) > 1e-9 {
			t.Fatalf("seed %d: TeamValue %v, lineup-based %v", seed, got, want)
		}
	}
}

// Property: the exact optimum is never worse than greedy, always legal, and
// strictly better often enough to justify its existence.
func TestBestSquad_NeverLosesToGreedyAndSometimesWins(t *testing.T) {
	positions := []fpl.Position{fpl.GKP, fpl.DEF, fpl.DEF, fpl.DEF, fpl.MID, fpl.MID, fpl.MID, fpl.FWD, fpl.FWD}
	score := func(p fpl.Player) float64 { return float64(p.TotalPoints) }
	compared, better := 0, 0

	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var pool []fpl.Player
		for id := 1; id <= 120+rng.Intn(120); id++ {
			p := mk(id, 1+rng.Intn(20), positions[rng.Intn(len(positions))], 40+rng.Intn(100), rng.Intn(150))
			if rng.Intn(10) == 0 {
				p.Status = "i"
			}
			pool = append(pool, p)
		}
		greedy, err := optimizer.Greedy(pool, byPoints)
		if err != nil {
			continue
		}
		var eligible []fpl.Player
		for _, p := range pool {
			if optimizer.Available(p) {
				eligible = append(eligible, p)
			}
		}

		r, err := optimizer.BestSquad(candidatesFrom(eligible), optimizer.Budget)
		if err != nil {
			t.Fatalf("seed %d: greedy found a squad but BestSquad failed: %v", seed, err)
		}
		if verr := optimizer.Validate(r.Squad); verr != nil {
			t.Fatalf("seed %d: illegal squad: %v", seed, verr)
		}
		g := optimizer.TeamValue(greedy.Players, score)
		compared++
		if r.Value < g-1e-9 {
			t.Fatalf("seed %d: exact %v is worse than greedy %v", seed, r.Value, g)
		}
		if r.Value > g+1e-9 {
			better++
		}
	}
	if compared < 30 {
		t.Fatalf("only %d instances compared", compared)
	}
	if better == 0 {
		t.Error("exact was never strictly better than greedy; either greedy is already optimal here or something is wrong")
	}
	t.Logf("exact beat greedy in %d of %d random instances", better, compared)
}
