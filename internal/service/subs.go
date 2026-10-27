package service

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// maxSubs bounds a plan: more swaps than this is picking a new XI, which Best XI does better.
const maxSubs = 4

// SubsSwapRequest brings a bench player on for a starter.
type SubsSwapRequest struct {
	Out int `json:"out"` // a starter's player id
	In  int `json:"in"`  // a bench player's id
}

// SubsRequest is a set of substitutions to weigh for the upcoming gameweek.
type SubsRequest struct {
	Swaps []SubsSwapRequest `json:"swaps"`
}

// SubsSwapView is one substitution and what it is expected to change.
type SubsSwapView struct {
	Out   PlayerView `json:"out"`
	In    PlayerView `json:"in"`
	Delta float64    `json:"delta"` // the incoming player's expected points minus the outgoing one's
}

// SubsView is the verdict on substitutions: whether FPL allows them and what they do to the XI's
// expected points this gameweek. Every number is worked out here, in Go.
type SubsView struct {
	Gameweek int      `json:"gameweek"`
	Valid    bool     `json:"valid"`
	Problems []string `json:"problems"` // why it is not allowed; nothing below is worked out then

	Swaps           []SubsSwapView `json:"swaps"`
	FormationBefore string         `json:"formation_before"`
	Formation       string         `json:"formation"`
	Before          float64        `json:"expected_points_before"` // the XI as set, captain counted twice
	After           float64        `json:"expected_points_after"`
	Delta           float64        `json:"expected_points_change"`
	Best            float64        `json:"best_expected_points"` // the best XI of the squad
	BestFormation   string         `json:"best_formation"`
	VsBest          float64        `json:"expected_points_vs_best"` // after minus best: zero or negative
	CaptainBefore   string         `json:"captain_before"`
	Captain         string         `json:"captain"`
	ViceCaptain     string         `json:"vice_captain"`
	Starting        []PlayerView   `json:"starting"` // after the swaps
	Bench           []PlayerView   `json:"bench"`    // after the swaps, in auto-sub order
	Insights        []string       `json:"insights"`
	XPBasis         string         `json:"xp_basis"`
	Notes           []string       `json:"notes"`
}

// Subs weighs substitutions in the manager's squad: a starter swapped with a bench player, as on
// FPL's Pick Team page. It checks FPL's rules (a goalkeeper for a goalkeeper; a formation FPL
// allows) and reports the XI's expected points before and after, against the best XI, where the
// armband goes, and the risks the swaps take or remove. It changes nothing on FPL.
func (s *Service) Subs(ctx context.Context, req SubsRequest) (SubsView, error) {
	if len(req.Swaps) > maxSubs {
		return SubsView{}, badInput("at most %d substitutions at a time: for more, use Best XI", maxSubs)
	}
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return SubsView{}, err
	}
	squad := sq.team.Squad
	byID := make(map[int]fpl.Player, len(squad.Players))
	for _, p := range squad.Players {
		byID[p.ID] = p
	}
	starting := slices.Clone(squad.Starting)
	var bench []int // in the order FPL brings them on
	for _, p := range squad.Players {
		if !slices.Contains(starting, p.ID) {
			bench = append(bench, p.ID)
		}
	}
	xp := base.model.ScoreFrom(base.next, 1)
	v := SubsView{
		Gameweek: base.next, Problems: []string{}, Swaps: []SubsSwapView{}, Insights: []string{},
		Notes:   joinNotes(base.notes, sq.notes),
		XPBasis: fmt.Sprintf("each player's expected points in GW%d only", base.next),
	}

	// The rules, in order: who may swap, then what the XI must look like.
	seen := map[int]bool{}
	for _, sw := range req.Swaps {
		out, okOut := byID[sw.Out]
		in, okIn := byID[sw.In]
		switch {
		case !okOut:
			return SubsView{}, badInput("player %d is not in your squad", sw.Out)
		case !okIn:
			return SubsView{}, badInput("player %d is not in your squad", sw.In)
		case seen[sw.Out] || seen[sw.In]:
			v.Problems = append(v.Problems, fmt.Sprintf("%s or %s is in more than one swap.", out.WebName, in.WebName))
		case !slices.Contains(starting, sw.Out):
			v.Problems = append(v.Problems, fmt.Sprintf("%s is on the bench already: pick a starter to take off.", out.WebName))
		case slices.Contains(starting, sw.In):
			v.Problems = append(v.Problems, fmt.Sprintf("%s already starts: pick a bench player to bring on.", in.WebName))
		case (out.Position == fpl.GKP) != (in.Position == fpl.GKP):
			v.Problems = append(v.Problems, fmt.Sprintf("A goalkeeper can only swap with a goalkeeper: %s and %s cannot swap.", out.WebName, in.WebName))
		}
		seen[sw.Out], seen[sw.In] = true, true
	}
	after, afterBench := slices.Clone(starting), slices.Clone(bench)
	if len(v.Problems) == 0 {
		for _, sw := range req.Swaps {
			after[slices.Index(after, sw.Out)] = sw.In
			afterBench[slices.Index(afterBench, sw.In)] = sw.Out // he takes the bench place of the one who came on
		}
		if f, ok := formationOf(after, byID); !ok {
			v.Problems = append(v.Problems, fmt.Sprintf("That would play %s, which FPL does not allow: %s.", f, formationRule()))
		}
	}
	if len(v.Problems) > 0 {
		return v, nil
	}
	v.Valid = true

	players := func(ids []int) []fpl.Player {
		out := make([]fpl.Player, len(ids))
		for i, id := range ids {
			out[i] = byID[id]
		}
		return out
	}
	captainBefore, _ := armbandFor(players(starting), squad.Captain, squad.ViceCaptain, xp)
	captain, vice := armbandFor(players(after), squad.Captain, squad.ViceCaptain, xp)
	v.Before = round1(xiTotal(players(starting), captainBefore, xp))
	v.After = round1(xiTotal(players(after), captain, xp))
	v.Delta = round1(v.After - v.Before)
	v.FormationBefore, _ = formationOf(starting, byID)
	v.Formation, _ = formationOf(after, byID)
	v.CaptainBefore, v.Captain, v.ViceCaptain = captainBefore.WebName, captain.WebName, vice.WebName
	for _, p := range players(after) {
		v.Starting = append(v.Starting, base.view(p, xp(p)))
	}
	for _, p := range players(afterBench) {
		v.Bench = append(v.Bench, base.view(p, xp(p)))
	}
	if best, err := lineup.Advise(squad.Players, lineup.Options{XP: xp}); err == nil {
		v.Best, v.BestFormation = round1(best.Total), best.Formation
		v.VsBest = round1(math.Min(0, v.After-v.Best))
	}
	for _, sw := range req.Swaps {
		out, in := byID[sw.Out], byID[sw.In]
		v.Swaps = append(v.Swaps, SubsSwapView{Out: base.view(out, xp(out)), In: base.view(in, xp(in)), Delta: round1(xp(in) - xp(out))})
	}
	v.Insights = subsInsights(v, players(afterBench))
	return v, nil
}

// formationOf reads an XI's shape ("4-4-2") and whether FPL allows it.
func formationOf(xi []int, byID map[int]fpl.Player) (string, bool) {
	var n [4]int
	for _, id := range xi {
		n[byID[id].Position-fpl.GKP]++
	}
	name := fmt.Sprintf("%d-%d-%d", n[1], n[2], n[3])
	if n[0] != 1 {
		return name, false
	}
	for _, f := range optimizer.LegalFormations() {
		if f == [3]int{n[1], n[2], n[3]} {
			return name, true
		}
	}
	return name, false
}

// formationRule says FPL's minimums, read from the legal formations rather than restated.
func formationRule() string {
	mins := [3]int{99, 99, 99}
	for _, f := range optimizer.LegalFormations() {
		for i := range mins {
			mins[i] = min(mins[i], f[i])
		}
	}
	return fmt.Sprintf("a goalkeeper and at least %d defenders, %d midfielders and %d forward", mins[0], mins[1], mins[2])
}

// armbandFor is who captains an XI: the chosen captain if he starts, else the vice, else the
// best scorer; and a vice from the rest the same way.
func armbandFor(xi []fpl.Player, captainID, viceID int, xp func(fpl.Player) float64) (captain, vice fpl.Player) {
	ranked := slices.Clone(xi)
	slices.SortStableFunc(ranked, func(a, b fpl.Player) int {
		switch {
		case xp(a) > xp(b):
			return -1
		case xp(a) < xp(b):
			return 1
		}
		return 0
	})
	pick := func(prefer []int, not int) fpl.Player {
		for _, id := range prefer {
			for _, p := range xi {
				if p.ID == id && id != not {
					return p
				}
			}
		}
		for _, p := range ranked {
			if p.ID != not {
				return p
			}
		}
		return fpl.Player{}
	}
	captain = pick([]int{captainID, viceID}, 0)
	vice = pick([]int{viceID, captainID}, captain.ID)
	return captain, vice
}

func xiTotal(xi []fpl.Player, captain fpl.Player, xp func(fpl.Player) float64) float64 {
	total := xp(captain)
	for _, p := range xi {
		total += xp(p)
	}
	return total
}

func subsInsights(v SubsView, bench []fpl.Player) []string {
	var out []string
	if len(v.Swaps) == 0 {
		return append(out, "Pick a starter, then a bench player to bring on for him, and the swap is weighed here.")
	}
	for _, sw := range v.Swaps {
		verb := "gains"
		if sw.Delta < 0 {
			verb = "costs"
		}
		out = append(out, fmt.Sprintf("%s on for %s %s %.1f expected points (%s %s against %s %s).",
			sw.In.Name, sw.Out.Name, verb, math.Abs(sw.Delta), sw.In.Name, fixtureOr(sw.In), sw.Out.Name, fixtureOr(sw.Out)))
	}
	out = append(out, fmt.Sprintf("Your XI goes from %.1f to %.1f expected points (%+.1f).", v.Before, v.After, v.Delta))
	if v.Formation != v.FormationBefore {
		out = append(out, fmt.Sprintf("The formation changes from %s to %s.", v.FormationBefore, v.Formation))
	}
	if v.Captain != v.CaptainBefore {
		out = append(out, fmt.Sprintf("%s goes to the bench, so the armband goes to %s.", v.CaptainBefore, v.Captain))
	}
	if v.Best > 0 {
		if v.VsBest > -0.05 {
			out = append(out, fmt.Sprintf("This matches the best XI for GW%d.", v.Gameweek))
		} else {
			out = append(out, fmt.Sprintf("The best XI for GW%d (%s) is %.1f: this one is %.1f behind it.", v.Gameweek, v.BestFormation, v.Best, -v.VsBest))
		}
	}
	for _, sw := range v.Swaps {
		// A doubtful player's expected points already allow for the chance he misses out, and FPL
		// would bring a bench player on if he did, so neither side of the swap is "safe" for free.
		for _, p := range []PlayerView{sw.In, sw.Out} {
			if !strings.HasPrefix(p.Status, "available") {
				out = append(out, fmt.Sprintf("%s is %s: his %.1f already allows for the chance he misses out, and if he starts and does not play, FPL brings a bench player on.", p.Name, p.Status, p.XP))
			}
		}
	}
	for _, p := range bench {
		if p.Position != fpl.GKP {
			out = append(out, fmt.Sprintf("If an outfield starter does not play, FPL brings on %s first.", p.WebName))
			break
		}
	}
	return out
}

func fixtureOr(p PlayerView) string {
	if p.Fixture == "" {
		return "with no fixture"
	}
	return p.Fixture
}
