// Package lineup picks the best starting XI from a squad for one gameweek.
//
// A transfer decision looks several gameweeks ahead; a lineup is chosen for a
// single match week, so it ranks players by their expected points in just that
// gameweek. It also reports how close each call was: a bench player 0.1 expected
// points behind a starter is a coin flip, and should read as one.
package lineup

import (
	"errors"
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// defaultCloseCall is the expected-points gap under which a benching is a toss-up.
const defaultCloseCall = 0.5

// ErrInvalidSquad means the 15 players are not a legal FPL squad.
var ErrInvalidSquad = errors.New("invalid squad")

// ErrUnknownFormation means Options.Formation is not one FPL allows.
var ErrUnknownFormation = errors.New("unknown formation")

// ErrNotStarting means a chosen captain or vice-captain is not in the starting XI. FPL
// only lets a player who starts wear the armband.
var ErrNotStarting = errors.New("not in the starting XI")

// NotStartingError says which player was chosen for the armband without starting, and in what role.
// It satisfies errors.Is(err, ErrNotStarting).
type NotStartingError struct {
	ID   int
	Role string // "captain" or "vice-captain"
}

func (e *NotStartingError) Error() string {
	return fmt.Sprintf("%s: player %d cannot be %s", ErrNotStarting, e.ID, e.Role)
}

func (e *NotStartingError) Is(target error) bool { return target == ErrNotStarting }

// ErrSameCaptain means the captain and the vice-captain are the same player.
var ErrSameCaptain = errors.New("captain and vice-captain are the same player")

// Options configures Advise.
type Options struct {
	// XP is each player's expected points for the gameweek. Required.
	XP func(fpl.Player) float64
	// CloseCallMargin is the largest expected-points gap reported as a close call;
	// default 0.5.
	CloseCallMargin float64
	// Formation, such as "3-4-3", forces that formation instead of the best one. The
	// captain, bench and close calls are then worked out within it; Formations still
	// lists every legal one, compared with the best overall.
	Formation string
	// Captain and ViceCaptain, as player ids, choose who wears the armband instead of the two best
	// scorers. Each must start. Setting one leaves the other to be the best remaining choice.
	Captain, ViceCaptain int
}

// Entry is a player with his expected points.
type Entry struct {
	Player fpl.Player
	XP     float64
}

// CloseCall says how nearly a benched player started: Margin is how many
// expected points the lineup would lose by starting him, at the cost of
// Displaces, the best starter who would make way.
type CloseCall struct {
	Bench     Entry
	Displaces Entry
	Margin    float64
}

// FormationOption is the best XI for one formation.
type FormationOption struct {
	Formation string
	Total     float64
	Delta     float64 // Total minus the best formation's total; 0 or negative
}

// Advice is the recommended lineup.
type Advice struct {
	Formation   string  // "4-4-2": defenders, midfielders, forwards
	Starting    []Entry // goalkeeper, then defenders, midfielders, forwards; best first within each
	Bench       []Entry // spare goalkeeper first, then outfielders by expected points
	Captain     Entry
	ViceCaptain Entry
	Total       float64 // expected points of the XI, with the captain counted twice
	// BestCaptain and BestTotal are what the two best scorers would give: Total can only be lower
	// when the manager chose a different captain.
	BestCaptain Entry
	BestTotal   float64
	CloseCalls  []CloseCall
	Formations  []FormationOption // every legal formation, best first
}

// Advise picks the XI that maximises expected points, with the captain's score
// counted twice. It is exact: every legal formation is tried and, within one,
// the top scorers of each position start. Ties go to the lower player id, so the
// result does not depend on the order of squad. squad is not modified.
func Advise(squad []fpl.Player, o Options) (Advice, error) {
	if o.XP == nil {
		return Advice{}, errors.New("lineup: an expected-points function is required")
	}
	if err := check(squad); err != nil {
		return Advice{}, err
	}
	margin := o.CloseCallMargin
	if margin <= 0 {
		margin = defaultCloseCall
	}

	var byPos [5][]Entry
	for _, p := range squad {
		byPos[p.Position] = append(byPos[p.Position], Entry{Player: p, XP: o.XP(p)})
	}
	for pos := range byPos {
		sortEntries(byPos[pos])
	}

	var only *[3]int
	if o.Formation != "" {
		f, ok := parseFormation(o.Formation)
		if !ok {
			return Advice{}, fmt.Errorf("%w: %q", ErrUnknownFormation, o.Formation)
		}
		only = &f
	}
	best, ok := bestXI(byPos, 0, only)
	if !ok {
		return Advice{}, fmt.Errorf("%w: no legal XI", ErrInvalidSquad)
	}
	overall := best // the best of every formation, for comparing the chosen one with
	if only != nil {
		if overall, ok = bestXI(byPos, 0, nil); !ok {
			return Advice{}, fmt.Errorf("%w: no legal XI", ErrInvalidSquad)
		}
	}

	started := map[int]bool{}
	for _, e := range best.xi {
		started[e.Player.ID] = true
	}
	var bench []Entry
	for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
		for _, e := range byPos[pos] {
			if !started[e.Player.ID] {
				bench = append(bench, e)
			}
		}
	}
	// The spare goalkeeper always sits first; outfielders follow by expected points.
	sort.SliceStable(bench, func(i, j int) bool {
		gi, gj := bench[i].Player.Position == fpl.GKP, bench[j].Player.Position == fpl.GKP
		if gi != gj {
			return gi
		}
		return less(bench[i], bench[j])
	})

	ranked := append([]Entry(nil), best.xi...)
	sortEntries(ranked)
	captain, vice, err := armband(ranked, o.Captain, o.ViceCaptain)
	if err != nil {
		return Advice{}, err
	}
	advice := Advice{
		Formation:   best.name,
		Starting:    best.xi,
		Bench:       bench,
		Captain:     captain,
		ViceCaptain: vice,
		// The XI is the same whoever captains it: only the captain's extra score changes.
		Total:       best.total - ranked[0].XP + captain.XP,
		BestCaptain: ranked[0],
		BestTotal:   best.total,
	}

	for _, b := range bench {
		alt, ok := bestXI(byPos, b.Player.ID, only)
		if !ok {
			continue
		}
		gap := best.total - alt.total
		if gap < 0 {
			gap = 0
		}
		if gap > margin {
			continue
		}
		advice.CloseCalls = append(advice.CloseCalls, CloseCall{Bench: b, Displaces: displaced(best.xi, alt.xi), Margin: gap})
	}
	sort.SliceStable(advice.CloseCalls, func(i, j int) bool {
		a, b := advice.CloseCalls[i], advice.CloseCalls[j]
		if a.Margin != b.Margin {
			return a.Margin < b.Margin
		}
		return a.Bench.Player.ID < b.Bench.Player.ID
	})

	for _, f := range optimizer.LegalFormations() {
		if x, ok := bestForFormation(byPos, f, 0); ok {
			advice.Formations = append(advice.Formations, FormationOption{Formation: x.name, Total: x.total, Delta: x.total - overall.total})
		}
	}
	sort.SliceStable(advice.Formations, func(i, j int) bool { return advice.Formations[i].Total > advice.Formations[j].Total })
	return advice, nil
}

// Changes compares two lineups by player id and returns who came in and who went
// out, each ascending.
func Changes(previous, advised []int) (in, out []int) {
	was, is := map[int]bool{}, map[int]bool{}
	for _, id := range previous {
		was[id] = true
	}
	for _, id := range advised {
		is[id] = true
		if !was[id] {
			in = append(in, id)
		}
	}
	for _, id := range previous {
		if !is[id] {
			out = append(out, id)
		}
	}
	sort.Ints(in)
	sort.Ints(out)
	return in, out
}

type xi struct {
	name  string
	xi    []Entry
	total float64
}

// parseFormation reads "3-4-3" and checks FPL allows it.
func parseFormation(name string) ([3]int, bool) {
	var f [3]int
	if n, err := fmt.Sscanf(name, "%d-%d-%d", &f[0], &f[1], &f[2]); err != nil || n != 3 || fmt.Sprintf("%d-%d-%d", f[0], f[1], f[2]) != name {
		return f, false
	}
	for _, legal := range optimizer.LegalFormations() {
		if legal == f {
			return f, true
		}
	}
	return f, false
}

// bestXI is the best lineup over all formations, or over just one when only is set; if
// forced is a player id, he must start.
func bestXI(byPos [5][]Entry, forced int, only *[3]int) (xi, bool) {
	var best xi
	found := false
	for _, f := range optimizer.LegalFormations() {
		if only != nil && f != *only {
			continue
		}
		if x, ok := bestForFormation(byPos, f, forced); ok && (!found || x.total > best.total) {
			best, found = x, true
		}
	}
	return best, found
}

// bestForFormation takes the top scorers of each position, swapping the forced
// player in for the lowest of them if he is not already among them.
func bestForFormation(byPos [5][]Entry, f [3]int, forced int) (xi, bool) {
	counts := [5]int{0, 1, f[0], f[1], f[2]}
	var lineup []Entry
	for pos := fpl.GKP; pos <= fpl.FWD; pos++ {
		if counts[pos] > len(byPos[pos]) {
			return xi{}, false
		}
		pick := append([]Entry(nil), byPos[pos][:counts[pos]]...)
		for _, e := range byPos[pos] {
			if e.Player.ID != forced {
				continue
			}
			if !containsID(pick, forced) {
				pick[len(pick)-1] = e
			}
		}
		lineup = append(lineup, pick...)
	}
	total, top := 0.0, lineup[0].XP
	for _, e := range lineup {
		total += e.XP
		if e.XP > top {
			top = e.XP
		}
	}
	return xi{name: fmt.Sprintf("%d-%d-%d", f[0], f[1], f[2]), xi: lineup, total: total + top}, true
}

// displaced is the best starter in the recommended XI who is absent from the alternative.
func displaced(best, alt []Entry) Entry {
	var out []Entry
	for _, e := range best {
		if !containsID(alt, e.Player.ID) {
			out = append(out, e)
		}
	}
	sortEntries(out)
	if len(out) == 0 {
		return Entry{}
	}
	return out[0]
}

func containsID(es []Entry, id int) bool {
	for _, e := range es {
		if e.Player.ID == id {
			return true
		}
	}
	return false
}

// less orders by expected points, best first, then by lower player id.
func less(a, b Entry) bool {
	if a.XP != b.XP {
		return a.XP > b.XP
	}
	return a.Player.ID < b.Player.ID
}

func sortEntries(es []Entry) { sort.Slice(es, func(i, j int) bool { return less(es[i], es[j]) }) }

// check confirms the squad is 15 distinct players split 2/5/5/3.
func check(squad []fpl.Player) error {
	if len(squad) != 15 {
		return fmt.Errorf("%w: have %d players, need 15", ErrInvalidSquad, len(squad))
	}
	want := map[fpl.Position]int{fpl.GKP: 2, fpl.DEF: 5, fpl.MID: 5, fpl.FWD: 3}
	have := map[fpl.Position]int{}
	seen := map[int]bool{}
	for _, p := range squad {
		if seen[p.ID] {
			return fmt.Errorf("%w: player %d appears twice", ErrInvalidSquad, p.ID)
		}
		seen[p.ID] = true
		have[p.Position]++
	}
	for pos, n := range want {
		if have[pos] != n {
			return fmt.Errorf("%w: have %d %s, need %d", ErrInvalidSquad, have[pos], pos, n)
		}
	}
	return nil
}

// armband picks the captain and vice-captain from the XI, best first. A chosen player must be in it.
// With one chosen, the other is the best of the rest.
func armband(ranked []Entry, captainID, viceID int) (captain, vice Entry, err error) {
	find := func(id int) (Entry, bool) {
		for _, e := range ranked {
			if e.Player.ID == id {
				return e, true
			}
		}
		return Entry{}, false
	}
	var ok bool
	if captainID != 0 {
		if captain, ok = find(captainID); !ok {
			return Entry{}, Entry{}, &NotStartingError{ID: captainID, Role: "captain"}
		}
	}
	if viceID != 0 {
		if vice, ok = find(viceID); !ok {
			return Entry{}, Entry{}, &NotStartingError{ID: viceID, Role: "vice-captain"}
		}
	}
	if captainID != 0 && captainID == viceID {
		return Entry{}, Entry{}, ErrSameCaptain
	}
	for _, e := range ranked {
		switch {
		case captainID == 0 && e.Player.ID != viceID:
			captain, captainID = e, e.Player.ID
		case viceID == 0 && e.Player.ID != captainID:
			vice, viceID = e, e.Player.ID
		}
	}
	return captain, vice, nil
}
