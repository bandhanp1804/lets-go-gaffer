package optimizer_test

import (
	"errors"
	"math/rand"
	"slices"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// byPoints scores a player by TotalPoints, which keeps test fixtures readable.
var byPoints = optimizer.Options{Score: func(p fpl.Player) float64 { return float64(p.TotalPoints) }}

func mk(id, team int, pos fpl.Position, cost, pts int) fpl.Player {
	return fpl.Player{ID: id, TeamID: team, Position: pos, NowCost: cost, TotalPoints: pts, Status: "a"}
}

func ids(ps []fpl.Player) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	slices.Sort(out)
	return out
}

func startersAt(s fpl.Squad, pos fpl.Position) int {
	byID := map[int]fpl.Position{}
	for _, p := range s.Players {
		byID[p.ID] = p.Position
	}
	n := 0
	for _, id := range s.Starting {
		if byID[id] == pos {
			n++
		}
	}
	return n
}

func mustGreedy(t *testing.T, pool []fpl.Player, opts optimizer.Options) fpl.Squad {
	t.Helper()
	s, err := optimizer.Greedy(pool, opts)
	if err != nil {
		t.Fatalf("Greedy: %v", err)
	}
	if err := optimizer.Validate(s); err != nil {
		t.Fatalf("result is not a legal squad: %v", err)
	}
	return s
}

// ample is a pool where money and clubs never bind: every player is on their own
// club and costs 40. Scores fall with id inside each position, and each position
// has its own score band so that no two formations ever tie on total score.
func ample() []fpl.Player {
	var pool []fpl.Player
	add := func(pos fpl.Position, first, n, top int) {
		for i := 0; i < n; i++ {
			pool = append(pool, mk(first+i, first+i, pos, 40, top-i))
		}
	}
	add(fpl.GKP, 1, 4, 100)
	add(fpl.DEF, 11, 8, 80)
	add(fpl.MID, 21, 8, 90)
	add(fpl.FWD, 31, 5, 100)
	return pool
}

func TestGreedy_PicksTheBestPlayersPerPositionWhenNothingBinds(t *testing.T) {
	s := mustGreedy(t, ample(), byPoints)

	want := []int{1, 2, 11, 12, 13, 14, 15, 21, 22, 23, 24, 25, 31, 32, 33}
	if got := ids(s.Players); !slices.Equal(got, want) {
		t.Errorf("squad = %v, want the top 2/5/5/3 by score %v", got, want)
	}
}

func TestGreedy_RespectsTheClubLimit(t *testing.T) {
	pool := ample()
	// The five best midfielders are all at club 100.
	for i := range pool {
		if pool[i].Position == fpl.MID && pool[i].ID <= 25 {
			pool[i].TeamID = 100
		}
	}

	s := mustGreedy(t, pool, byPoints)

	var mids []int
	for _, p := range s.Players {
		if p.Position == fpl.MID {
			mids = append(mids, p.ID)
		}
	}
	slices.Sort(mids)
	// Three from club 100 (the best three), then the next best from elsewhere.
	if want := []int{21, 22, 23, 26, 27}; !slices.Equal(mids, want) {
		t.Errorf("midfielders = %v, want %v", mids, want)
	}
}

func TestGreedy_LeavesRoomToFinishTheSquad(t *testing.T) {
	// Fifteen stars at 70 cost 1050 and would bust the budget; worse, a naive
	// greedy takes 14 of them (980) and then cannot afford any 15th player.
	var pool []fpl.Player
	id := 1
	add := func(pos fpl.Position, stars, fodder int) {
		for i := 0; i < stars; i++ {
			pool = append(pool, mk(id, id, pos, 70, 100-id))
			id++
		}
		for i := 0; i < fodder; i++ {
			pool = append(pool, mk(id, id, pos, 40, 1))
			id++
		}
	}
	add(fpl.GKP, 3, 2)
	add(fpl.DEF, 6, 2)
	add(fpl.MID, 6, 2)
	add(fpl.FWD, 4, 2)

	s := mustGreedy(t, pool, byPoints)

	stars := 0
	for _, p := range s.Players {
		if p.NowCost == 70 {
			stars++
		}
	}
	// 70k + 40(15-k) <= 1000 gives k <= 13: the most stars any legal squad can hold.
	if stars != 13 {
		t.Errorf("stars = %d, want 13 (the maximum the budget allows)", stars)
	}
}

func TestGreedy_NeverPicksUnavailablePlayers(t *testing.T) {
	pool := ample()
	// Make the best player in each position injured or suspended.
	banned := map[int]bool{1: true, 11: true, 21: true, 31: true}
	for i := range pool {
		if banned[pool[i].ID] {
			pool[i].Status = "i"
			pool[i].TotalPoints = 1000
		}
	}

	s := mustGreedy(t, pool, byPoints)

	for _, p := range s.Players {
		if banned[p.ID] {
			t.Errorf("selected unavailable player %d", p.ID)
		}
	}
}

func TestGreedy_ChoosesTheBestFormation(t *testing.T) {
	tests := []struct {
		name          string
		boost         func(p *fpl.Player)
		def, mid, fwd int // expected starters
	}{
		{"midfield heavy gives 3-5-2", func(p *fpl.Player) {
			if p.Position == fpl.MID {
				p.TotalPoints += 500
			}
		}, 3, 5, 2},
		{"forward heavy gives 3-4-3", func(p *fpl.Player) {
			if p.Position == fpl.FWD {
				p.TotalPoints += 500
			}
		}, 3, 4, 3},
		{"defence heavy gives 5-2-3", func(p *fpl.Player) {
			if p.Position == fpl.DEF {
				p.TotalPoints += 500
			}
		}, 5, 2, 3}, // 5-2-3 beats 5-3-2: a third forward (98) outscores a third midfielder (88)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool := ample()
			for i := range pool {
				tc.boost(&pool[i])
			}

			s := mustGreedy(t, pool, byPoints)

			got := [3]int{startersAt(s, fpl.DEF), startersAt(s, fpl.MID), startersAt(s, fpl.FWD)}
			if got != [3]int{tc.def, tc.mid, tc.fwd} {
				t.Errorf("formation = %d-%d-%d, want %d-%d-%d", got[0], got[1], got[2], tc.def, tc.mid, tc.fwd)
			}
			if startersAt(s, fpl.GKP) != 1 {
				t.Error("want exactly one starting goalkeeper")
			}
		})
	}
}

func TestGreedy_CaptainIsTheTopStarterAndViceIsSecond(t *testing.T) {
	pool := ample()
	for i := range pool {
		switch pool[i].ID {
		case 22:
			pool[i].TotalPoints = 900 // best player overall
		case 31:
			pool[i].TotalPoints = 800 // second best
		}
	}

	s := mustGreedy(t, pool, byPoints)

	if s.Captain != 22 || s.ViceCaptain != 31 {
		t.Errorf("captain/vice = %d/%d, want 22/31", s.Captain, s.ViceCaptain)
	}
}

func TestGreedy_IsDeterministicWhateverTheInputOrder(t *testing.T) {
	// Everyone ties on score, so only the tie-break (lowest id) decides.
	pool := ample()
	for i := range pool {
		pool[i].TotalPoints = 50
	}
	first := mustGreedy(t, slices.Clone(pool), byPoints)

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		shuffled := slices.Clone(pool)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got := mustGreedy(t, shuffled, byPoints)

		if !slices.Equal(ids(got.Players), ids(first.Players)) ||
			!slices.Equal(got.Starting, first.Starting) || got.Captain != first.Captain {
			t.Fatalf("run %d differs from the first result", i)
		}
	}
}

func TestGreedy_DoesNotMutateTheInputPool(t *testing.T) {
	pool := ample()
	before := slices.Clone(pool)

	mustGreedy(t, pool, byPoints)

	if !slices.Equal(pool, before) {
		t.Error("Greedy reordered or modified the caller's slice")
	}
}

func TestGreedy_ReportsInfeasiblePools(t *testing.T) {
	tooPricey := ample()
	for i := range tooPricey {
		tooPricey[i].NowCost = 100 // fifteen of them cost 1500
	}
	noForwards := slices.DeleteFunc(ample(), func(p fpl.Player) bool { return p.Position == fpl.FWD })
	tooFewKeepers := slices.DeleteFunc(ample(), func(p fpl.Player) bool { return p.Position == fpl.GKP && p.ID > 1 })

	tests := []struct {
		name string
		pool []fpl.Player
	}{
		{"empty pool", nil},
		{"cheapest legal squad is over budget", tooPricey},
		{"no forwards at all", noForwards},
		{"only one goalkeeper", tooFewKeepers},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := optimizer.Greedy(tc.pool, byPoints)

			if !errors.Is(err, optimizer.ErrInfeasible) {
				t.Fatalf("err = %v, want ErrInfeasible", err)
			}
			if len(s.Players) != 0 {
				t.Errorf("returned a partial squad of %d players alongside the error", len(s.Players))
			}
		})
	}
}

func TestGreedy_DefaultsUseFormAndAvailability(t *testing.T) {
	pool := ample()
	for i := range pool {
		pool[i].Form = "1.0"
		pool[i].TotalPoints = 0
		if pool[i].ID == 12 { // a defender with a hot form but not the lowest id
			pool[i].Form = "9.5"
		}
		if pool[i].ID == 11 {
			pool[i].Status = "u"
		}
	}

	s, err := optimizer.Greedy(pool, optimizer.Options{}) // nil Score and Eligible

	if err != nil {
		t.Fatal(err)
	}
	got := ids(s.Players)
	if !slices.Contains(got, 12) {
		t.Error("the in-form defender should be selected (default score is form)")
	}
	if slices.Contains(got, 11) {
		t.Error("an unavailable player was selected (default eligibility is status a)")
	}
}

// Property: whatever the pool, a successful result is always a legal squad.
func TestGreedy_AlwaysReturnsALegalSquad(t *testing.T) {
	positions := []fpl.Position{fpl.GKP, fpl.DEF, fpl.DEF, fpl.DEF, fpl.MID, fpl.MID, fpl.MID, fpl.FWD, fpl.FWD}

	const seeds = 300
	built := 0
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var pool []fpl.Player
		for id := 1; id <= 100+rng.Intn(300); id++ {
			p := mk(id, 1+rng.Intn(20), positions[rng.Intn(len(positions))], 40+rng.Intn(100), rng.Intn(150))
			if rng.Intn(10) == 0 {
				p.Status = "i"
			}
			pool = append(pool, p)
		}

		s, err := optimizer.Greedy(pool, byPoints)
		if errors.Is(err, optimizer.ErrInfeasible) {
			continue // legitimately impossible pools are allowed to fail, never to cheat
		}
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if verr := optimizer.Validate(s); verr != nil {
			t.Fatalf("seed %d produced an illegal squad: %v", seed, verr)
		}
		built++
	}
	// Guard against a vacuous pass: if nearly every pool were "infeasible" the
	// loop above would check nothing.
	if built < seeds*9/10 {
		t.Errorf("only %d of %d random pools produced a squad; the property was barely exercised", built, seeds)
	}
}
