package transfers_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

func byPoints(p fpl.Player) float64 { return float64(p.TotalPoints) }

func player(id, team int, pos fpl.Position, cost, score int) fpl.Player {
	return fpl.Player{ID: id, TeamID: team, Position: pos, NowCost: cost, TotalPoints: score, Status: "a"}
}

// squad15 is a legal squad: GKP 1-2, DEF 3-7, MID 8-12, FWD 13-15; club
// (id-1)%5+1, so clubs 1-5 each have exactly three players (the club limit).
// Everyone costs 5.0m and scores 5, except player 10 who scores 20 and is the
// captain, so that an upgrade never changes who the captain is.
func squad15() []fpl.Player {
	pos := func(id int) fpl.Position {
		switch {
		case id <= 2:
			return fpl.GKP
		case id <= 7:
			return fpl.DEF
		case id <= 12:
			return fpl.MID
		}
		return fpl.FWD
	}
	var out []fpl.Player
	for id := 1; id <= 15; id++ {
		score := 5
		if id == 10 {
			score = 20
		}
		out = append(out, player(id, (id-1)%5+1, pos(id), 50, score))
	}
	return out
}

// sellAtCost sells every player for his listed price (no profit or loss).
func sellAtCost(ps []fpl.Player) map[int]int {
	m := map[int]int{}
	for _, p := range ps {
		m[p.ID] = p.NowCost
	}
	return m
}

func input(bank int, pool ...fpl.Player) transfers.Input {
	sq := squad15()
	return transfers.Input{Squad: sq, Sell: sellAtCost(sq), Bank: bank, Pool: pool}
}

func opts(free, horizon int) transfers.Options {
	return transfers.Options{Score: byPoints, FreeTransfers: free, Horizon: horizon, TopN: 20, Candidates: 12}
}

func outs(s transfers.Suggestion) []int {
	var o []int
	for _, m := range s.Moves {
		o = append(o, m.Out.ID)
	}
	return o
}

func ins(s transfers.Suggestion) []int {
	var o []int
	for _, m := range s.Moves {
		o = append(o, m.In.ID)
	}
	return o
}

func suggest(t *testing.T, in transfers.Input, o transfers.Options) []transfers.Suggestion {
	t.Helper()
	got, err := transfers.Suggest(in, o)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	return got
}

func TestSuggest_FindsTheUpgradeThatFitsTheBudget(t *testing.T) {
	// 101 is a 55 goalkeeper scoring 9; 102 scores more but costs far too much.
	in := input(5, player(101, 51, fpl.GKP, 55, 9), player(102, 52, fpl.GKP, 140, 12))

	got := suggest(t, in, opts(1, 2))

	if len(got) == 0 {
		t.Fatal("want a suggestion")
	}
	top := got[0]
	// Replacing either goalkeeper gains the same, so the lower id wins the tie.
	if !slices.Equal(outs(top), []int{1}) || !slices.Equal(ins(top), []int{101}) {
		t.Errorf("top = out %v in %v, want out [1] in [101]", outs(top), ins(top))
	}
	if top.Transfers != 1 || top.Hit != 0 {
		t.Errorf("transfers=%d hit=%d, want 1 and 0 (one free transfer)", top.Transfers, top.Hit)
	}
	if top.ScoreGain != 4 {
		t.Errorf("gain = %v per gameweek, want 4 (a 5 becomes a 9)", top.ScoreGain)
	}
	if top.Net != 8 { // 4 per gameweek over a horizon of 2, no hit
		t.Errorf("net = %v, want 8", top.Net)
	}
	if top.CostChange != 5 || top.BankAfter != 0 {
		t.Errorf("cost change %d, bank after %d; want spend 5 and bank 0", top.CostChange, top.BankAfter)
	}
	for _, s := range got {
		if slices.Contains(ins(s), 102) {
			t.Error("suggested a player the bank cannot pay for")
		}
	}
}

func TestSuggest_AHitHasToBeWorthIt(t *testing.T) {
	in := func() transfers.Input { return input(5, player(101, 51, fpl.GKP, 55, 9)) }

	// No free transfer: the move costs 4 points. A 4-point weekly gain over one week breaks even.
	if got := suggest(t, in(), opts(0, 1)); len(got) != 0 {
		t.Errorf("a move that only breaks even after its hit was suggested: %+v", got[0])
	}
	// Over two weeks it earns 8 against a 4 point hit.
	got := suggest(t, in(), opts(0, 2))
	if len(got) == 0 || got[0].Hit != 4 || got[0].Net != 4 {
		t.Errorf("got %+v, want a move with a 4 point hit and net 4", got)
	}
	// With a free transfer the same move has no hit at all.
	if got := suggest(t, in(), opts(1, 2)); got[0].Hit != 0 || got[0].Net != 8 {
		t.Errorf("hit %d net %v, want 0 and 8", got[0].Hit, got[0].Net)
	}
}

func TestSuggest_TwoTransfersOnlyWhenTheSecondPaysForItself(t *testing.T) {
	// Two independent upgrades, each worth 4 a week: a goalkeeper and a defender.
	pool := []fpl.Player{player(101, 51, fpl.GKP, 50, 9), player(103, 53, fpl.DEF, 50, 9)}
	best := func(free, horizon int) transfers.Suggestion {
		return suggest(t, input(0, pool...), opts(free, horizon))[0]
	}

	// One free transfer, one week: a single move nets 4; both nets 8 - 4 = 4. A tie
	// goes to the simpler plan.
	if b := best(1, 1); b.Transfers != 1 {
		t.Errorf("tie: top has %d transfers, want the single move", b.Transfers)
	}
	// Over three weeks the second move earns 12 for a 4 point hit.
	if b := best(1, 3); b.Transfers != 2 || b.Hit != 4 || b.Net != 20 {
		t.Errorf("3 weeks: transfers=%d hit=%d net=%v, want 2, 4, 20", b.Transfers, b.Hit, b.Net)
	}
	// With two free transfers there is no hit, so both moves win at once.
	if b := best(2, 1); b.Transfers != 2 || b.Hit != 0 || b.Net != 8 {
		t.Errorf("2 free: transfers=%d hit=%d net=%v, want 2, 0, 8", b.Transfers, b.Hit, b.Net)
	}
}

func TestSuggest_ResultsAreRankedBestFirst(t *testing.T) {
	pool := []fpl.Player{player(101, 51, fpl.GKP, 50, 9), player(103, 53, fpl.DEF, 50, 8), player(104, 54, fpl.DEF, 50, 7)}

	got := suggest(t, input(0, pool...), opts(2, 2))

	for i := 1; i < len(got); i++ {
		if got[i-1].Net < got[i].Net {
			t.Fatalf("suggestion %d (net %v) ranks above %d (net %v)", i-1, got[i-1].Net, i, got[i].Net)
		}
	}
	if len(got) > 20 {
		t.Errorf("returned %d, want at most TopN", len(got))
	}
}

func TestSuggest_BudgetUsesSellingPricesAndIsExactAtTheBoundary(t *testing.T) {
	in := input(0, player(101, 51, fpl.GKP, 50, 9))
	if got := suggest(t, in, opts(1, 2)); len(got) == 0 {
		t.Fatal("a swap costing exactly what the outgoing player sells for must be allowed")
	}

	// Player 1 sells for 48, not his market 50 (the manager bought high or the price rose less).
	in = input(0, player(101, 51, fpl.GKP, 50, 9))
	in.Sell[1], in.Sell[2] = 48, 48
	if got := suggest(t, in, opts(1, 2)); len(got) != 0 {
		t.Errorf("a 50 player cannot be bought from a 48 sale and an empty bank: %+v", got[0])
	}
	// Two tenths in the bank is exactly enough.
	in.Bank = 2
	if got := suggest(t, in, opts(1, 2)); len(got) == 0 {
		t.Error("48 + 2 in the bank buys a 50 player")
	}
}

func TestSuggest_RespectsTheClubLimit(t *testing.T) {
	// Club 1 already has three players (ids 1, 6, 11). Player 101 is also club 1.
	in := input(0, player(101, 1, fpl.GKP, 50, 9))

	got := suggest(t, in, opts(1, 2))

	if len(got) == 0 {
		t.Fatal("selling the club-1 goalkeeper (id 1) for another club-1 goalkeeper keeps three: legal")
	}
	for _, s := range got {
		if slices.Contains(outs(s), 2) {
			t.Errorf("replacing goalkeeper 2 (club 2) with a club-1 player makes four from one club: %+v", s)
		}
	}
	if !slices.Equal(outs(got[0]), []int{1}) {
		t.Errorf("out = %v, want [1]", outs(got[0]))
	}
}

func TestSuggest_NeverBuysUnavailableOrAlreadyOwnedPlayers(t *testing.T) {
	injured := player(104, 54, fpl.GKP, 50, 99)
	injured.Status = "i"
	owned := squad15()[2] // player 3, already in the squad, offered again in the pool
	owned.TotalPoints = 99
	in := input(0, injured, owned, player(101, 51, fpl.GKP, 50, 9))

	got := suggest(t, in, opts(1, 2))

	for _, s := range got {
		if slices.Contains(ins(s), 104) || slices.Contains(ins(s), 3) {
			t.Errorf("bought an injured or already-owned player: %+v", s)
		}
	}
}

func TestSuggest_NothingWorthDoingIsNotAnError(t *testing.T) {
	got, err := transfers.Suggest(input(100, player(101, 51, fpl.GKP, 50, 3), player(103, 53, fpl.DEF, 50, 2)), opts(1, 4))

	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no suggestions and no error: keep the squad as it is", got, err)
	}
}

func TestSuggest_IsDeterministicWhateverThePoolOrder(t *testing.T) {
	pool := []fpl.Player{
		player(101, 51, fpl.GKP, 50, 9), player(105, 55, fpl.GKP, 50, 9),
		player(103, 53, fpl.DEF, 50, 9), player(106, 56, fpl.DEF, 50, 9),
	}
	first := suggest(t, input(0, pool...), opts(1, 3))

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 15; i++ {
		shuffled := slices.Clone(pool)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got := suggest(t, input(0, shuffled...), opts(1, 3))

		if len(got) != len(first) {
			t.Fatalf("run %d returned %d suggestions, first run %d", i, len(got), len(first))
		}
		for j := range got {
			if !slices.Equal(outs(got[j]), outs(first[j])) || !slices.Equal(ins(got[j]), ins(first[j])) {
				t.Fatalf("run %d suggestion %d differs", i, j)
			}
		}
	}
}

func TestSuggest_DoesNotMutateItsInputs(t *testing.T) {
	in := input(5, player(101, 51, fpl.GKP, 55, 9), player(103, 53, fpl.DEF, 50, 9))
	squadBefore, poolBefore := slices.Clone(in.Squad), slices.Clone(in.Pool)
	sellBefore := map[int]int{}
	for k, v := range in.Sell {
		sellBefore[k] = v
	}

	suggest(t, in, opts(1, 3))

	if !slices.Equal(in.Squad, squadBefore) || !slices.Equal(in.Pool, poolBefore) {
		t.Error("Suggest modified the caller's slices")
	}
	for k, v := range sellBefore {
		if in.Sell[k] != v {
			t.Error("Suggest modified the caller's sell prices")
		}
	}
}

func TestSuggest_RejectsInvalidInput(t *testing.T) {
	good := func() transfers.Input { return input(0, player(101, 51, fpl.GKP, 50, 9)) }
	tests := []struct {
		name   string
		mutate func(in *transfers.Input, o *transfers.Options)
	}{
		{"fewer than 15 players", func(in *transfers.Input, _ *transfers.Options) { in.Squad = in.Squad[:14] }},
		{"missing selling price", func(in *transfers.Input, _ *transfers.Options) { delete(in.Sell, 7) }},
		{"negative bank", func(in *transfers.Input, _ *transfers.Options) { in.Bank = -1 }},
		{"negative free transfers", func(_ *transfers.Input, o *transfers.Options) { o.FreeTransfers = -1 }},
		{"horizon below one", func(_ *transfers.Input, o *transfers.Options) { o.Horizon = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, o := good(), opts(1, 2)
			tc.mutate(&in, &o)

			if _, err := transfers.Suggest(in, o); err == nil {
				t.Error("want an error")
			}
		})
	}
}

// Property: every suggestion, whatever the pool, is a legal squad the manager can
// afford, with the arithmetic reported honestly.
func TestSuggest_EverySuggestionIsLegalAffordableAndAddsUp(t *testing.T) {
	positions := []fpl.Position{fpl.GKP, fpl.DEF, fpl.DEF, fpl.MID, fpl.MID, fpl.FWD}
	const free, horizon = 1, 3
	checked := 0

	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var pool []fpl.Player
		for id := 100; id < 100+80; id++ {
			pool = append(pool, player(id, 1+rng.Intn(12), positions[rng.Intn(len(positions))], 40+rng.Intn(60), rng.Intn(15)))
		}
		in := input(rng.Intn(15), pool...)
		for i := range in.Squad {
			in.Squad[i].TeamID = 1 + rng.Intn(12) // scatter clubs, keeping the starting squad legal below
		}
		if optimizer.ValidateWithBudget(fpl.Squad{Players: in.Squad, Starting: []int{1, 3, 4, 5, 6, 8, 9, 10, 11, 13, 14}, Captain: 10, ViceCaptain: 8}, optimizer.NoBudgetLimit) != nil {
			continue // random clubs broke the 3-per-club rule; the squad is not a valid starting point
		}

		for _, s := range suggest(t, in, opts(free, horizon)) {
			checked++
			if err := optimizer.ValidateWithBudget(s.Squad, optimizer.NoBudgetLimit); err != nil {
				t.Fatalf("seed %d: illegal squad: %v", seed, err)
			}
			if s.BankAfter < 0 {
				t.Fatalf("seed %d: spends money the manager does not have (bank after %d)", seed, s.BankAfter)
			}
			var in15, sold int
			for _, m := range s.Moves {
				in15 += m.In.NowCost
				sold += m.SellPrice
			}
			if s.BankAfter != in.Bank+sold-in15 || s.CostChange != in15-sold {
				t.Fatalf("seed %d: money does not add up: bank %d + sold %d - bought %d != %d", seed, in.Bank, sold, in15, s.BankAfter)
			}
			if want := 4 * max(0, s.Transfers-free); s.Hit != want || s.Transfers != len(s.Moves) {
				t.Fatalf("seed %d: hit %d for %d transfers, want %d", seed, s.Hit, s.Transfers, want)
			}
			if math.Abs(s.Net-(horizon*s.ScoreGain-float64(s.Hit))) > 1e-9 || s.Net <= 0 {
				t.Fatalf("seed %d: net %v inconsistent with gain %v and hit %d", seed, s.Net, s.ScoreGain, s.Hit)
			}
			want := transfers.TeamScore(s.Squad.Players, byPoints) - transfers.TeamScore(in.Squad, byPoints)
			if math.Abs(s.ScoreGain-want) > 1e-9 {
				t.Fatalf("seed %d: reported gain %v, recomputed %v", seed, s.ScoreGain, want)
			}
		}
	}
	if checked < 50 {
		t.Errorf("only %d suggestions were checked; the property was barely exercised", checked)
	}
}

func TestWildcard_RebuildsFreelyWithNoHit(t *testing.T) {
	// A squad of weak players, and a pool of far better ones at the same price.
	in := input(0)
	for i := range in.Squad {
		if in.Squad[i].ID != 10 {
			in.Squad[i].TotalPoints = 1
		}
	}
	id := 200
	for _, pos := range []fpl.Position{fpl.GKP, fpl.GKP, fpl.DEF, fpl.DEF, fpl.DEF, fpl.DEF, fpl.DEF, fpl.MID, fpl.MID, fpl.MID, fpl.MID, fpl.FWD, fpl.FWD, fpl.FWD} {
		in.Pool = append(in.Pool, player(id, id, pos, 50, 10)) // each on its own club
		id++
	}

	w, err := transfers.Wildcard(in, opts(1, 4))

	if err != nil {
		t.Fatal(err)
	}
	if w.Transfers < 8 {
		t.Errorf("wildcard made only %d transfers; with 14 strictly better players on offer it should rebuild most of the squad", w.Transfers)
	}
	if w.Hit != 0 {
		t.Errorf("a wildcard has no hit, got %d", w.Hit)
	}
	if err := optimizer.ValidateWithBudget(w.Squad, optimizer.NoBudgetLimit); err != nil {
		t.Errorf("wildcard squad illegal: %v", err)
	}
	if w.BankAfter < 0 {
		t.Errorf("wildcard overspent: bank after %d", w.BankAfter)
	}
	// The best normal two-transfer plan can never beat an unrestricted rebuild.
	if normal := suggest(t, in, opts(2, 1)); len(normal) > 0 && w.ScoreGain < normal[0].ScoreGain {
		t.Errorf("wildcard gain %v is below the best 2-transfer gain %v", w.ScoreGain, normal[0].ScoreGain)
	}
}

func TestWildcard_LeavesAGoodSquadAlone(t *testing.T) {
	w, err := transfers.Wildcard(input(0, player(101, 51, fpl.GKP, 50, 3)), opts(1, 4))

	if err != nil {
		t.Fatal(err)
	}
	if w.Transfers != 0 || w.ScoreGain != 0 {
		t.Errorf("transfers=%d gain=%v; nothing in the pool is better, so nothing should change", w.Transfers, w.ScoreGain)
	}
}

func TestSuggest_SurfacesAnInfeasibleSquadClearly(t *testing.T) {
	in := input(0, player(101, 51, fpl.GKP, 50, 9))
	in.Squad[0].Position = fpl.DEF // 1 GKP, 6 DEF: not a legal split

	_, err := transfers.Suggest(in, opts(1, 2))

	if err == nil || !errors.Is(err, transfers.ErrInvalidSquad) {
		t.Errorf("err = %v, want ErrInvalidSquad", err)
	}
}
