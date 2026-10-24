package lineup_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sort"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// squad15 is GKP 1-2, DEF 3-7, MID 8-12, FWD 13-15.
func squad15() []fpl.Player {
	var out []fpl.Player
	for id := 1; id <= 15; id++ {
		pos := fpl.FWD
		switch {
		case id <= 2:
			pos = fpl.GKP
		case id <= 7:
			pos = fpl.DEF
		case id <= 12:
			pos = fpl.MID
		}
		out = append(out, fpl.Player{ID: id, WebName: "P", TeamID: 1 + id%5, Position: pos})
	}
	return out
}

// xpOf scores players from a table; unlisted players score 1.
func xpOf(table map[int]float64) lineup.Options {
	return lineup.Options{XP: func(p fpl.Player) float64 {
		if v, ok := table[p.ID]; ok {
			return v
		}
		return 1
	}}
}

func advise(t *testing.T, squad []fpl.Player, o lineup.Options) lineup.Advice {
	t.Helper()
	a, err := lineup.Advise(squad, o)
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}
	return a
}

func idsOf(es []lineup.Entry) []int {
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = e.Player.ID
	}
	return out
}

// bruteForce tries every legal XI. It returns the best total (captain counted
// twice) and, per player, the best total among XIs that include him.
func bruteForce(squad []fpl.Player, xp func(fpl.Player) float64) (best float64, bestWith map[int]float64) {
	byPos := map[fpl.Position][]fpl.Player{}
	for _, p := range squad {
		byPos[p.Position] = append(byPos[p.Position], p)
	}
	best = math.Inf(-1)
	bestWith = map[int]float64{}
	choose := func(ps []fpl.Player, k int, f func([]fpl.Player)) {
		var rec func(start int, picked []fpl.Player)
		rec = func(start int, picked []fpl.Player) {
			if len(picked) == k {
				f(picked)
				return
			}
			for i := start; i < len(ps); i++ {
				rec(i+1, append(picked[:len(picked):len(picked)], ps[i]))
			}
		}
		rec(0, nil)
	}
	for _, form := range optimizer.LegalFormations() {
		for _, gk := range byPos[fpl.GKP] {
			choose(byPos[fpl.DEF], form[0], func(d []fpl.Player) {
				choose(byPos[fpl.MID], form[1], func(m []fpl.Player) {
					choose(byPos[fpl.FWD], form[2], func(f []fpl.Player) {
						xi := append(append(append([]fpl.Player{gk}, d...), m...), f...)
						total, top := 0.0, math.Inf(-1)
						for _, p := range xi {
							total += xp(p)
							top = math.Max(top, xp(p))
						}
						total += top
						best = math.Max(best, total)
						for _, p := range xi {
							if total > bestWith[p.ID] {
								bestWith[p.ID] = total
							}
						}
					})
				})
			})
		}
	}
	return best, bestWith
}

func TestAdvise_MatchesBruteForceOverEveryLegalXI(t *testing.T) {
	for seed := int64(0); seed < 80; seed++ {
		rng := rand.New(rand.NewSource(seed))
		table := map[int]float64{}
		for id := 1; id <= 15; id++ {
			table[id] = float64(rng.Intn(100)) / 10 // includes ties
		}
		o := xpOf(table)
		squad := squad15()
		want, _ := bruteForce(squad, o.XP)

		a := advise(t, squad, o)

		if math.Abs(a.Total-want) > 1e-9 {
			t.Fatalf("seed %d: advised total %v, true best %v", seed, a.Total, want)
		}
		if len(a.Starting) != 11 || len(a.Bench) != 4 {
			t.Fatalf("seed %d: %d starters and %d on the bench, want 11 and 4", seed, len(a.Starting), len(a.Bench))
		}
	}
}

func TestAdvise_CloseCallMarginsAreExact(t *testing.T) {
	// For each bench player, the margin is how much worse the best XI containing
	// him is than the overall best. It is checked against complete enumeration.
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(500 + seed))
		table := map[int]float64{}
		for id := 1; id <= 15; id++ {
			table[id] = float64(rng.Intn(60)) / 10
		}
		o := xpOf(table)
		o.CloseCallMargin = 1000 // report every bench player so each can be checked
		squad := squad15()
		best, bestWith := bruteForce(squad, o.XP)

		a := advise(t, squad, o)

		if len(a.CloseCalls) != 4 {
			t.Fatalf("seed %d: %d close calls, want one for each of the 4 bench players", seed, len(a.CloseCalls))
		}
		for _, c := range a.CloseCalls {
			want := best - bestWith[c.Bench.Player.ID]
			if math.Abs(c.Margin-want) > 1e-9 {
				t.Fatalf("seed %d: margin for player %d = %v, brute force says %v", seed, c.Bench.Player.ID, c.Margin, want)
			}
			if c.Margin < -1e-12 {
				t.Fatalf("seed %d: negative margin %v", seed, c.Margin)
			}
		}
		if !sort.SliceIsSorted(a.CloseCalls, func(i, j int) bool { return a.CloseCalls[i].Margin < a.CloseCalls[j].Margin }) {
			t.Errorf("seed %d: close calls are not ordered closest first", seed)
		}
	}
}

func TestAdvise_CloseCallsListOnlyTossUps(t *testing.T) {
	// Defender 7 sits on the bench 0.1 behind the weakest starting defender; midfielder 12 is far behind.
	table := map[int]float64{
		1: 5, 2: 1,
		3: 6, 4: 6, 5: 6, 6: 5, 7: 4.9,
		8: 7, 9: 7, 10: 7, 11: 7, 12: 0.5,
		13: 8, 14: 8, 15: 1,
	}

	a := advise(t, squad15(), xpOf(table))

	if len(a.CloseCalls) == 0 || a.CloseCalls[0].Bench.Player.ID != 7 || a.CloseCalls[0].Displaces.Player.ID != 6 {
		t.Fatalf("close calls = %+v, want player 7 narrowly missing out to player 6 first", a.CloseCalls)
	}
	if m := a.CloseCalls[0].Margin; math.Abs(m-0.1) > 1e-9 {
		t.Errorf("margin = %v, want 0.1", m)
	}
	for _, c := range a.CloseCalls {
		if c.Bench.Player.ID == 12 {
			t.Errorf("a player 6.5 xP short is not a close call: %+v", c)
		}
		if c.Margin > 0.5 {
			t.Errorf("margin %v is above the default 0.5 threshold", c.Margin)
		}
	}
}

func TestAdvise_ChoosesTheFormationThatFitsTheScores(t *testing.T) {
	// Forwards and midfielders far out-score defenders: attack-minded formation.
	table := map[int]float64{
		1: 4, 2: 1,
		3: 2, 4: 2, 5: 2, 6: 2, 7: 2,
		8: 6, 9: 6, 10: 6, 11: 6, 12: 6,
		13: 9, 14: 9, 15: 9,
	}

	a := advise(t, squad15(), xpOf(table))

	if a.Formation != "3-4-3" && a.Formation != "3-5-2" {
		t.Errorf("formation = %s, want a three-at-the-back shape when defenders are the weakest", a.Formation)
	}
	if a.Formation == "3-5-2" {
		t.Errorf("3-4-3 (a 9 up front) beats 3-5-2 (a 6): got %s", a.Formation)
	}
	if len(a.Formations) != 8 {
		t.Fatalf("formations listed = %d, want all 8", len(a.Formations))
	}
	if a.Formations[0].Formation != a.Formation || a.Formations[0].Delta != 0 {
		t.Errorf("the best formation should lead the list with delta 0: %+v", a.Formations[0])
	}
	for i := 1; i < len(a.Formations); i++ {
		if a.Formations[i].Total > a.Formations[i-1].Total || a.Formations[i].Delta > 0 {
			t.Errorf("formations are not ordered best first: %+v", a.Formations)
		}
	}
}

func TestAdvise_CaptainAndViceAreTheTwoBestStarters(t *testing.T) {
	table := map[int]float64{1: 5, 8: 9.5, 9: 9, 13: 9, 14: 8}

	a := advise(t, squad15(), xpOf(table))

	if a.Captain.Player.ID != 8 || a.ViceCaptain.Player.ID != 9 {
		t.Errorf("captain %d vice %d, want 8 and the tied 9 (lower id wins the tie)", a.Captain.Player.ID, a.ViceCaptain.Player.ID)
	}
	// A captain scores double, so he is in the total twice.
	var sum float64
	for _, e := range a.Starting {
		sum += e.XP
	}
	if want := sum + a.Captain.XP; math.Abs(a.Total-want) > 1e-9 {
		t.Errorf("total = %v, want XI %v plus the captain again = %v", a.Total, sum, want)
	}
}

func TestAdvise_BenchOrder(t *testing.T) {
	table := map[int]float64{
		1: 5, 2: 3, // 2 is the spare goalkeeper
		3: 6, 4: 6, 5: 6, 6: 1.5, 7: 2, // two defenders will not start
		8: 7, 9: 7, 10: 7, 11: 7, 12: 7,
		13: 8, 14: 8, 15: 1,
	}

	a := advise(t, squad15(), xpOf(table))

	if len(a.Bench) != 4 || a.Bench[0].Player.ID != 2 || a.Bench[0].Player.Position != fpl.GKP {
		t.Fatalf("bench = %v, want the spare goalkeeper (2) first", idsOf(a.Bench))
	}
	rest := a.Bench[1:]
	if !sort.SliceIsSorted(rest, func(i, j int) bool { return rest[i].XP > rest[j].XP }) {
		t.Errorf("outfield bench %v is not ordered by expected points, best first", idsOf(rest))
	}
}

func TestAdvise_AnInjuredPlayerDoesNotStartWhileAnAlternativeExists(t *testing.T) {
	// Player 8 is the squad's best form but expected to score nothing (injured).
	table := map[int]float64{8: 0}

	a := advise(t, squad15(), xpOf(table))

	if slices.Contains(idsOf(a.Starting), 8) {
		t.Errorf("a player expected to score 0 started over healthy alternatives: %v", idsOf(a.Starting))
	}
}

func TestAdvise_IsDeterministicWhateverTheInputOrder(t *testing.T) {
	o := xpOf(map[int]float64{}) // every player ties on 1
	first := advise(t, squad15(), o)

	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 20; i++ {
		shuffled := squad15()
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got := advise(t, shuffled, o)

		if !slices.Equal(idsOf(got.Starting), idsOf(first.Starting)) || !slices.Equal(idsOf(got.Bench), idsOf(first.Bench)) ||
			got.Captain.Player.ID != first.Captain.Player.ID || got.Formation != first.Formation {
			t.Fatalf("run %d differs from the first", i)
		}
	}
}

func TestAdvise_DoesNotMutateTheSquad(t *testing.T) {
	squad := squad15()
	before := slices.Clone(squad)

	advise(t, squad, xpOf(map[int]float64{8: 9, 3: 0}))

	if !slices.Equal(squad, before) {
		t.Error("Advise reordered or modified the caller's slice")
	}
}

func TestAdvise_RejectsSquadsThatAreNotLegal(t *testing.T) {
	good := squad15()
	dup := slices.Clone(good)
	dup[14].ID = dup[13].ID
	wrongSplit := slices.Clone(good)
	wrongSplit[0].Position = fpl.DEF

	tests := []struct {
		name  string
		squad []fpl.Player
		opts  lineup.Options
	}{
		{"fourteen players", good[:14], xpOf(nil)},
		{"duplicate player", dup, xpOf(nil)},
		{"wrong positional split", wrongSplit, xpOf(nil)},
		{"no scoring function", good, lineup.Options{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := lineup.Advise(tc.squad, tc.opts); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestChanges(t *testing.T) {
	tests := []struct {
		name         string
		prev, now    []int
		wantIn, want []int
	}{
		{"identical", []int{1, 2, 3}, []int{3, 2, 1}, nil, nil},
		{"one swap", []int{1, 2, 3}, []int{1, 2, 9}, []int{9}, []int{3}},
		{"two swaps, ids ascending", []int{5, 6, 7}, []int{9, 8, 7}, []int{8, 9}, []int{5, 6}},
		{"nothing before", nil, []int{2, 1}, []int{1, 2}, nil},
	}
	for _, tc := range tests {
		in, out := lineup.Changes(tc.prev, tc.now)
		if !slices.Equal(in, tc.wantIn) || !slices.Equal(out, tc.want) {
			t.Errorf("%s: in %v out %v, want in %v out %v", tc.name, in, out, tc.wantIn, tc.want)
		}
	}
}

func TestAdvise_AForcedFormationGivesThatFormationsBestXI(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		table := map[int]float64{}
		for id := 1; id <= 15; id++ {
			table[id] = math.Round(r.Float64()*100) / 10
		}
		free := advise(t, squad15(), xpOf(table))

		for _, f := range free.Formations {
			o := xpOf(table)
			o.Formation = f.Formation
			forced := advise(t, squad15(), o)

			if forced.Formation != f.Formation {
				t.Fatalf("asked for %s, got %s", f.Formation, forced.Formation)
			}
			if math.Abs(forced.Total-f.Total) > 1e-9 {
				t.Errorf("%s: forced total %v, but the unforced list says %v", f.Formation, forced.Total, f.Total)
			}
			// The list still compares every formation with the best overall, whichever is shown.
			for i, g := range forced.Formations {
				if g.Formation != free.Formations[i].Formation || math.Abs(g.Delta-free.Formations[i].Delta) > 1e-9 {
					t.Errorf("%s: formation list changed when forcing: %+v vs %+v", f.Formation, g, free.Formations[i])
				}
			}
			if len(forced.Starting) != 11 || len(forced.Bench) != 4 {
				t.Errorf("%s: starting %d bench %d", f.Formation, len(forced.Starting), len(forced.Bench))
			}
			// Captain and vice are the best two starters of the shown XI.
			if forced.Captain.XP < forced.ViceCaptain.XP {
				t.Errorf("%s: captain %v below vice %v", f.Formation, forced.Captain.XP, forced.ViceCaptain.XP)
			}
		}
	}
}

func TestAdvise_ForcingTheBestFormationChangesNothing(t *testing.T) {
	table := map[int]float64{1: 4, 3: 2, 4: 2, 8: 6, 9: 6, 13: 9, 14: 9, 15: 9}
	free := advise(t, squad15(), xpOf(table))
	o := xpOf(table)
	o.Formation = free.Formation

	forced := advise(t, squad15(), o)

	if forced.Total != free.Total || len(forced.CloseCalls) != len(free.CloseCalls) {
		t.Errorf("forced %+v vs free %+v", forced.Total, free.Total)
	}
}

func TestAdvise_UnknownFormationsAreRejected(t *testing.T) {
	for _, bad := range []string{"6-3-1", "4-4-3", "4-4", "x-y-z", "442", "4-4-2 ", "04-4-2", "3-4-3-0"} {
		o := xpOf(nil)
		o.Formation = bad
		if _, err := lineup.Advise(squad15(), o); !errors.Is(err, lineup.ErrUnknownFormation) {
			t.Errorf("%q: err = %v, want ErrUnknownFormation", bad, err)
		}
	}
}

func randomTable(r *rand.Rand) map[int]float64 {
	table := map[int]float64{}
	for id := 1; id <= 15; id++ {
		table[id] = math.Round(r.Float64()*100) / 10
	}
	return table
}

func TestAdvise_AChosenCaptainChangesOnlyTheCaptainsDoubleNotTheXI(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for trial := 0; trial < 30; trial++ {
		table := randomTable(r)
		free := advise(t, squad15(), xpOf(table))
		sum := 0.0
		for _, e := range free.Starting {
			sum += e.XP
		}

		for _, start := range free.Starting {
			o := xpOf(table)
			o.Captain = start.Player.ID
			got := advise(t, squad15(), o)

			if got.Captain.Player.ID != start.Player.ID {
				t.Fatalf("asked for captain %d, got %d", start.Player.ID, got.Captain.Player.ID)
			}
			if want := sum + start.XP; math.Abs(got.Total-want) > 1e-9 {
				t.Errorf("captain %d: total %v, want XI %v plus his %v = %v", start.Player.ID, got.Total, sum, start.XP, want)
			}
			if got.Total > got.BestTotal+1e-9 || math.Abs(got.BestTotal-free.Total) > 1e-9 || got.BestCaptain.Player.ID != free.Captain.Player.ID {
				t.Errorf("the best captain must be the recommended one and no choice can beat it: total %v best %v (free %v)", got.Total, got.BestTotal, free.Total)
			}
			for i, e := range got.Starting {
				if e.Player.ID != free.Starting[i].Player.ID {
					t.Fatalf("a captain choice must not change who starts")
				}
			}
			if got.ViceCaptain.Player.ID == got.Captain.Player.ID {
				t.Errorf("vice-captain %d is the captain", got.ViceCaptain.Player.ID)
			}
		}
	}
}

func TestAdvise_TheBestCaptainChoiceCostsNothing(t *testing.T) {
	table := map[int]float64{1: 4, 8: 9, 9: 8, 13: 7}
	free := advise(t, squad15(), xpOf(table))
	o := xpOf(table)
	o.Captain, o.ViceCaptain = free.Captain.Player.ID, free.ViceCaptain.Player.ID

	got := advise(t, squad15(), o)

	if got.Total != free.Total || got.Total != got.BestTotal {
		t.Errorf("choosing the recommended pair must change nothing: %v vs %v", got.Total, free.Total)
	}
}

func TestAdvise_AViceChosenAloneLeavesTheCaptainAsTheBestOfTheRest(t *testing.T) {
	table := map[int]float64{1: 4, 8: 9, 9: 8, 13: 7}
	free := advise(t, squad15(), xpOf(table))

	// Make the best player the vice: the next best must captain.
	o := xpOf(table)
	o.ViceCaptain = free.Captain.Player.ID
	got := advise(t, squad15(), o)
	if got.Captain.Player.ID != free.ViceCaptain.Player.ID || got.ViceCaptain.Player.ID != free.Captain.Player.ID {
		t.Errorf("captain %d vice %d, want them swapped (%d, %d)", got.Captain.Player.ID, got.ViceCaptain.Player.ID, free.ViceCaptain.Player.ID, free.Captain.Player.ID)
	}

	// Make the third best the captain: the best must be the vice.
	o = xpOf(table)
	third := free.Starting[len(free.Starting)-1].Player.ID
	o.Captain = third
	got = advise(t, squad15(), o)
	if got.Captain.Player.ID != third || got.ViceCaptain.Player.ID != free.Captain.Player.ID {
		t.Errorf("captain %d vice %d", got.Captain.Player.ID, got.ViceCaptain.Player.ID)
	}
}

func TestAdvise_ABenchPlayerCannotWearTheArmband(t *testing.T) {
	table := map[int]float64{1: 4, 8: 9, 9: 8, 13: 7}
	free := advise(t, squad15(), xpOf(table))
	bench := free.Bench[0].Player.ID
	for name, o := range map[string]lineup.Options{
		"captain": {XP: xpOf(table).XP, Captain: bench},
		"vice":    {XP: xpOf(table).XP, ViceCaptain: bench},
		"unknown": {XP: xpOf(table).XP, Captain: 999},
	} {
		if _, err := lineup.Advise(squad15(), o); !errors.Is(err, lineup.ErrNotStarting) {
			t.Errorf("%s: err = %v, want ErrNotStarting", name, err)
		}
	}
}

func TestAdvise_CaptainAndViceMustBeDifferentPlayers(t *testing.T) {
	table := map[int]float64{8: 9}
	o := xpOf(table)
	o.Captain, o.ViceCaptain = 8, 8
	if _, err := lineup.Advise(squad15(), o); !errors.Is(err, lineup.ErrSameCaptain) {
		t.Errorf("err = %v", err)
	}
}

func TestAdvise_ACaptainChoiceWorksWithAForcedFormation(t *testing.T) {
	table := map[int]float64{1: 4, 8: 9, 9: 8, 13: 7, 14: 6, 15: 5}
	o := xpOf(table)
	o.Formation = "3-4-3"
	first := advise(t, squad15(), o)
	o.Captain = first.Starting[len(first.Starting)-1].Player.ID

	got := advise(t, squad15(), o)

	if got.Formation != "3-4-3" || got.Captain.Player.ID != o.Captain || got.Total > got.BestTotal {
		t.Errorf("formation %s captain %d total %v best %v", got.Formation, got.Captain.Player.ID, got.Total, got.BestTotal)
	}
}
