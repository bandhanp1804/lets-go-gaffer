// Package optimizer holds the FPL squad rules and the squad optimizers.
//
// Everything here is plain Go with no I/O. The LLM never checks rules or does
// arithmetic; it only narrates the results computed here.
package optimizer

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

const (
	// Budget is £100.0m in tenths of millions, the unit of fpl.Player.NowCost.
	Budget       = 1000
	SquadSize    = 15
	StartingSize = 11
	MaxPerClub   = 3
)

// squadComposition is the required squad split by position.
var squadComposition = map[fpl.Position]int{fpl.GKP: 2, fpl.DEF: 5, fpl.MID: 5, fpl.FWD: 3}

// formationBounds is the legal [min, max] starters per position.
var formationBounds = map[fpl.Position][2]int{
	fpl.GKP: {1, 1},
	fpl.DEF: {3, 5},
	fpl.MID: {2, 5},
	fpl.FWD: {1, 3},
}

var positionOrder = []fpl.Position{fpl.GKP, fpl.DEF, fpl.MID, fpl.FWD}

// Rule names one FPL constraint.
type Rule string

const (
	RuleSquadSize          Rule = "squad_size"
	RulePositionCounts     Rule = "position_counts"
	RuleBudget             Rule = "budget"
	RuleClubLimit          Rule = "club_limit"
	RuleDuplicate          Rule = "duplicate_player"
	RuleStartingSize       Rule = "starting_size"
	RuleStartingNotInSquad Rule = "starting_not_in_squad"
	RuleFormation          Rule = "formation"
	RuleCaptain            Rule = "captain"
)

// Violation is one broken rule with a human-readable explanation.
type Violation struct {
	Rule   Rule
	Detail string
}

// ValidationError lists every rule a squad breaks, not only the first.
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = fmt.Sprintf("%s: %s", v.Rule, v.Detail)
	}
	return "invalid squad: " + strings.Join(parts, "; ")
}

// NoBudgetLimit disables the budget rule in ValidateWithBudget.
const NoBudgetLimit = math.MaxInt

// Validate checks s against the FPL rules, with the standard £100.0m budget, and
// returns a *ValidationError listing every violation, or nil if the squad is
// legal. All checks are independent, so one problem never hides another. It does
// not modify s.
func Validate(s fpl.Squad) error { return ValidateWithBudget(s, Budget) }

// ValidateWithBudget is Validate with a custom budget in tenths of £m. A squad
// that already exists can be worth more than £100.0m once prices have risen, so
// pass that manager's real funds, or NoBudgetLimit to skip the budget rule.
func ValidateWithBudget(s fpl.Squad, budget int) error {
	var vs []Violation
	add := func(r Rule, format string, args ...any) {
		vs = append(vs, Violation{Rule: r, Detail: fmt.Sprintf(format, args...)})
	}

	// Squad composition.
	if len(s.Players) != SquadSize {
		add(RuleSquadSize, "have %d players, need %d", len(s.Players), SquadSize)
	}

	byID := make(map[int]fpl.Player, len(s.Players))
	for _, id := range duplicates(playerIDs(s.Players)) {
		add(RuleDuplicate, "player %d appears more than once in the squad", id)
	}
	for _, p := range s.Players {
		byID[p.ID] = p
	}

	have := map[fpl.Position]int{}
	cost := 0
	perClub := map[int]int{}
	for _, p := range s.Players {
		have[p.Position]++
		cost += p.NowCost
		perClub[p.TeamID]++
	}
	if !samePositions(have) {
		add(RulePositionCounts, "have %s, need %s", formatCounts(have), formatCounts(squadComposition))
	}

	// Budget, in tenths of millions: integers only.
	if cost > budget {
		add(RuleBudget, "squad costs %d, budget is %d (tenths of £m); over by %d", cost, budget, cost-budget)
	}

	clubs := make([]int, 0, len(perClub))
	for id := range perClub {
		clubs = append(clubs, id)
	}
	sort.Ints(clubs)
	for _, id := range clubs {
		if perClub[id] > MaxPerClub {
			add(RuleClubLimit, "%d players from club %d, max is %d", perClub[id], id, MaxPerClub)
		}
	}

	// Starting XI.
	if len(s.Starting) != StartingSize {
		add(RuleStartingSize, "have %d starters, need %d", len(s.Starting), StartingSize)
	}
	for _, id := range duplicates(s.Starting) {
		add(RuleDuplicate, "player %d appears more than once in the starting XI", id)
	}

	started := map[fpl.Position]int{}
	inXI := map[int]bool{}
	var missing []int
	for _, id := range s.Starting {
		p, ok := byID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		inXI[id] = true
		started[p.Position]++
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		add(RuleStartingNotInSquad, "starters %v are not in the squad", missing)
	}
	for _, pos := range positionOrder {
		b := formationBounds[pos]
		if n := started[pos]; n < b[0] || n > b[1] {
			add(RuleFormation, "%d %s in the starting XI, allowed %d-%d", n, pos, b[0], b[1])
		}
	}

	// Captaincy.
	switch {
	case s.Captain == 0:
		add(RuleCaptain, "no captain set")
	case !inXI[s.Captain]:
		add(RuleCaptain, "captain %d is not in the starting XI", s.Captain)
	}
	switch {
	case s.ViceCaptain == 0:
		add(RuleCaptain, "no vice-captain set")
	case !inXI[s.ViceCaptain]:
		add(RuleCaptain, "vice-captain %d is not in the starting XI", s.ViceCaptain)
	case s.ViceCaptain == s.Captain:
		add(RuleCaptain, "captain and vice-captain are both player %d", s.Captain)
	}

	if len(vs) == 0 {
		return nil
	}
	return &ValidationError{Violations: vs}
}

func playerIDs(ps []fpl.Player) []int {
	ids := make([]int, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return ids
}

// duplicates returns each id that occurs more than once, ascending, once each.
func duplicates(ids []int) []int {
	seen := map[int]int{}
	for _, id := range ids {
		seen[id]++
	}
	var dups []int
	for id, n := range seen {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Ints(dups)
	return dups
}

func samePositions(have map[fpl.Position]int) bool {
	for pos, want := range squadComposition {
		if have[pos] != want {
			return false
		}
	}
	for pos := range have {
		if _, ok := squadComposition[pos]; !ok {
			return false // a player with an unknown position
		}
	}
	return true
}

func formatCounts(c map[fpl.Position]int) string {
	parts := make([]string, 0, len(positionOrder))
	for _, pos := range positionOrder {
		parts = append(parts, fmt.Sprintf("%d %s", c[pos], pos))
	}
	return strings.Join(parts, ", ")
}
