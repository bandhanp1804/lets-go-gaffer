package optimizer

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// ErrInfeasible means no legal squad can be built from the given players.
var ErrInfeasible = errors.New("no legal squad can be built from these players")

// Options customises Greedy. The zero value is usable.
type Options struct {
	// Score ranks players; higher is better. Nil uses FormScore. Plug in a real
	// expected-points model here without touching the optimizer.
	Score func(fpl.Player) float64
	// Eligible filters the pool. Nil uses Available.
	Eligible func(fpl.Player) bool
}

// FormScore scores a player by FPL's recent-form figure. Unparseable form is 0.
func FormScore(p fpl.Player) float64 {
	f, err := strconv.ParseFloat(p.Form, 64)
	if err != nil {
		return 0
	}
	return f
}

// Available reports whether FPL marks the player as fit to play.
func Available(p fpl.Player) bool { return p.Status == "a" }

// Greedy builds a legal squad and starting XI from players.
//
// It walks players from best score to worst and takes each one that fits the
// position quota and the club limit, and that still leaves enough budget to
// finish the squad with the cheapest legal fill. That last check is what stops
// it buying fourteen stars and then being unable to afford a fifteenth player.
//
// Greedy is a fast baseline, not an optimum: it commits to early picks. Ties
// break on the lower player id, so the result does not depend on input order.
// The input slice is not modified. The result always passes Validate.
func Greedy(players []fpl.Player, opts Options) (fpl.Squad, error) {
	score := opts.Score
	if score == nil {
		score = FormScore
	}
	eligible := opts.Eligible
	if eligible == nil {
		eligible = Available
	}

	scores := map[int]float64{}
	var pool []fpl.Player
	for _, p := range players {
		if eligible(p) {
			pool = append(pool, p)
			scores[p.ID] = score(p)
		}
	}

	byScore := append([]fpl.Player(nil), pool...)
	sort.Slice(byScore, func(i, j int) bool {
		a, b := byScore[i], byScore[j]
		if scores[a.ID] != scores[b.ID] {
			return scores[a.ID] > scores[b.ID]
		}
		return a.ID < b.ID
	})
	byCost := append([]fpl.Player(nil), pool...)
	sort.Slice(byCost, func(i, j int) bool {
		a, b := byCost[i], byCost[j]
		if a.NowCost != b.NowCost {
			return a.NowCost < b.NowCost
		}
		return a.ID < b.ID
	})

	st := newPickState()
	for _, p := range byScore {
		if len(st.picked) == SquadSize {
			break
		}
		if st.need[p.Position] == 0 || st.perClub[p.TeamID] >= MaxPerClub {
			continue
		}
		if !st.canFinishAfter(p, byCost) {
			continue
		}
		st.take(p)
	}
	if len(st.picked) != SquadSize {
		return fpl.Squad{}, fmt.Errorf("%w: found %d of %d players", ErrInfeasible, len(st.picked), SquadSize)
	}

	squad := pickLineup(st.picked, scores)
	// The constructive steps above should make this impossible; checking anyway
	// means a bug here becomes an error, never an illegal squad handed to a user.
	if err := Validate(squad); err != nil {
		return fpl.Squad{}, fmt.Errorf("optimizer built an invalid squad: %w", err)
	}
	return squad, nil
}

// pickState tracks a partially built squad.
type pickState struct {
	picked  []fpl.Player
	taken   map[int]bool
	need    map[fpl.Position]int // open slots per position
	perClub map[int]int
	cost    int
}

func newPickState() *pickState {
	need := make(map[fpl.Position]int, len(squadComposition))
	for pos, n := range squadComposition {
		need[pos] = n
	}
	return &pickState{taken: map[int]bool{}, need: need, perClub: map[int]int{}}
}

func (st *pickState) take(p fpl.Player) {
	st.picked = append(st.picked, p)
	st.taken[p.ID] = true
	st.need[p.Position]--
	st.perClub[p.TeamID]++
	st.cost += p.NowCost
}

// canFinishAfter reports whether, after taking p, the open slots can still be
// filled within budget. It prices the rest of the squad with the cheapest
// eligible players that respect the position quotas and the club limit.
func (st *pickState) canFinishAfter(p fpl.Player, byCost []fpl.Player) bool {
	left := Budget - st.cost - p.NowCost
	if left < 0 {
		return false
	}
	need := make(map[fpl.Position]int, len(st.need))
	open := 0
	for pos, n := range st.need {
		need[pos] = n
		open += n
	}
	need[p.Position]--
	open--
	perClub := make(map[int]int, len(st.perClub)+1)
	for id, n := range st.perClub {
		perClub[id] = n
	}
	perClub[p.TeamID]++

	for _, x := range byCost {
		if open == 0 {
			break
		}
		if x.ID == p.ID || st.taken[x.ID] || need[x.Position] <= 0 || perClub[x.TeamID] >= MaxPerClub {
			continue
		}
		left -= x.NowCost
		if left < 0 {
			return false
		}
		need[x.Position]--
		perClub[x.TeamID]++
		open--
	}
	return open == 0
}

// pickLineup chooses the highest-scoring legal starting XI from a full squad,
// then names the two best starters captain and vice-captain.
func pickLineup(picked []fpl.Player, scores map[int]float64) fpl.Squad {
	byPos := map[fpl.Position][]fpl.Player{}
	for _, p := range picked {
		byPos[p.Position] = append(byPos[p.Position], p)
	}
	rank := func(ps []fpl.Player) {
		sort.Slice(ps, func(i, j int) bool {
			if scores[ps[i].ID] != scores[ps[j].ID] {
				return scores[ps[i].ID] > scores[ps[j].ID]
			}
			return ps[i].ID < ps[j].ID
		})
	}
	for pos := range byPos {
		rank(byPos[pos])
	}

	sumTop := func(pos fpl.Position, n int) float64 {
		total := 0.0
		for _, p := range byPos[pos][:n] {
			total += scores[p.ID]
		}
		return total
	}

	// Try every legal formation: 1 GKP plus 10 outfielders.
	bestD, bestM, bestF := 0, 0, 0
	best := 0.0
	found := false
	for d := formationBounds[fpl.DEF][0]; d <= formationBounds[fpl.DEF][1]; d++ {
		for m := formationBounds[fpl.MID][0]; m <= formationBounds[fpl.MID][1]; m++ {
			f := StartingSize - 1 - d - m
			if f < formationBounds[fpl.FWD][0] || f > formationBounds[fpl.FWD][1] {
				continue
			}
			if d > len(byPos[fpl.DEF]) || m > len(byPos[fpl.MID]) || f > len(byPos[fpl.FWD]) {
				continue
			}
			total := sumTop(fpl.DEF, d) + sumTop(fpl.MID, m) + sumTop(fpl.FWD, f)
			if !found || total > best {
				best, bestD, bestM, bestF, found = total, d, m, f, true
			}
		}
	}

	var starters []fpl.Player
	starters = append(starters, byPos[fpl.GKP][:1]...)
	starters = append(starters, byPos[fpl.DEF][:bestD]...)
	starters = append(starters, byPos[fpl.MID][:bestM]...)
	starters = append(starters, byPos[fpl.FWD][:bestF]...)

	startingIDs := make([]int, len(starters))
	for i, p := range starters {
		startingIDs[i] = p.ID
	}
	rank(starters) // best first, to name captain and vice

	squad := fpl.Squad{Players: append([]fpl.Player(nil), picked...), Starting: startingIDs}
	sort.Slice(squad.Players, func(i, j int) bool {
		a, b := squad.Players[i], squad.Players[j]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.ID < b.ID
	})
	if len(starters) >= 2 {
		squad.Captain, squad.ViceCaptain = starters[0].ID, starters[1].ID
	}
	return squad
}
