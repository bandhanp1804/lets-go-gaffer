// Package projection estimates how many points a player will score, from the
// gameweek-by-gameweek record rather than a single recent-form number.
//
// A raw form figure is a poor forecast: a hot streak is partly luck, a handful
// of games says little, and it ignores both the minutes a player gets and who
// he plays next. The model corrects for each:
//
//   - Decay weights recent gameweeks more, without throwing the season away.
//   - PriorGames shrinks a thin record toward the position's average, in
//     proportion to how little the player has played.
//   - Minutes are modelled separately from points per 90, so a rotated or
//     recently injured player is expected to contribute less.
//   - FixtureSlope scales each match by FPL's difficulty rating, with a blank
//     gameweek worth nothing and a double worth both matches.
//
// The same code serves live predictions and the backtest, which only ever shows
// the model gameweeks before the one it predicts.
package projection

import (
	"fmt"
	"math"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// Params tunes the model.
type Params struct {
	// Decay is how much a gameweek counts relative to the one after it, in (0, 1].
	// 1 weights the whole season equally; 0.8 halves the weight about every three
	// gameweeks.
	Decay float64
	// MinutesDecay is the same, for how many minutes a player gets. Points per 90
	// are noisy and want a long memory; minutes reflect who is starting or has just
	// been dropped or injured, and want a short one. 0 means "same as Decay".
	MinutesDecay float64
	// PriorGames is the weight of the position average, in full 90-minute games.
	// 0 trusts a player's own record completely.
	PriorGames float64
	// FixtureSlope is the fraction by which each difficulty point away from 3
	// changes the expectation: 0.1 makes an easy (2) match worth 10% more and a
	// hard (4) one 10% less.
	FixtureSlope float64
}

// Default is the middle of the region that scored best in `gaffer backtest`, not
// its single best cell: nearby settings score almost identically, and a point
// picked from a few gameweeks of data would be fitting noise. Scored on
// gameweeks it was not tuned on (walk-forward) this family ranked players with a
// Spearman correlation of about 0.52, against 0.38 for last-three-games form.
var Default = Params{Decay: 0.85, MinutesDecay: 0.2, PriorGames: 5, FixtureSlope: 0.15}

const minMultiplier = 0.2

func (p Params) validate() error {
	switch {
	case p.Decay <= 0 || p.Decay > 1:
		return fmt.Errorf("projection: decay must be in (0, 1], got %v", p.Decay)
	case p.MinutesDecay < 0 || p.MinutesDecay > 1:
		return fmt.Errorf("projection: minutes decay must be in [0, 1], got %v", p.MinutesDecay)
	case p.PriorGames < 0:
		return fmt.Errorf("projection: prior games cannot be negative, got %v", p.PriorGames)
	case p.FixtureSlope < 0 || p.FixtureSlope > 0.5:
		return fmt.Errorf("projection: fixture slope must be in [0, 0.5], got %v", p.FixtureSlope)
	}
	return nil
}

// Model predicts points from gameweek next onward, using only earlier gameweeks.
type Model struct {
	params   Params
	next     int
	fixtures fpl.Fixtures
	perGame  map[int]float64 // player id -> expected points in a gameweek before the fixture adjustment
}

// New builds a model that predicts gameweek next and later. history rows from
// next onward are ignored, so it can be handed a whole season safely. players
// supplies each player's position. fixtures may be empty, in which case every
// match is treated as average difficulty.
func New(players []fpl.Player, history []fpl.PlayerHistory, fixtures fpl.Fixtures, p Params, next int) (*Model, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if next < 2 {
		return nil, fmt.Errorf("projection: cannot predict gameweek %d: there is no earlier history to learn from", next)
	}

	minutesDecay := p.MinutesDecay
	if minutesDecay == 0 {
		minutesDecay = p.Decay
	}
	// Weight of each elapsed gameweek: 1 for the latest, the decay for the one before, and so on.
	rateW, minW := make(map[int]float64, next-1), make(map[int]float64, next-1)
	var minTotal float64
	for g := 1; g < next; g++ {
		rateW[g] = math.Pow(p.Decay, float64(next-1-g))
		minW[g] = math.Pow(minutesDecay, float64(next-1-g))
		minTotal += minW[g]
	}

	// points and rateMinutes feed points per 90; shareMinutes feeds how much a player plays.
	type sums struct{ points, rateMinutes, shareMinutes float64 }
	byPlayer := make(map[int]*sums, len(players))
	for _, h := range history {
		wr, ok := rateW[h.GameweekID]
		if !ok {
			continue // from the gameweek being predicted onward: not allowed to see it
		}
		s := byPlayer[h.PlayerID]
		if s == nil {
			s = &sums{}
			byPlayer[h.PlayerID] = s
		}
		s.points += wr * float64(h.TotalPoints)
		s.rateMinutes += wr * float64(h.Minutes)
		s.shareMinutes += minW[h.GameweekID] * float64(h.Minutes)
	}

	// The position norm: points per 90 minutes across everyone who plays there.
	posPoints, posMinutes := map[fpl.Position]float64{}, map[fpl.Position]float64{}
	for _, pl := range players {
		if s := byPlayer[pl.ID]; s != nil {
			posPoints[pl.Position] += s.points
			posMinutes[pl.Position] += s.rateMinutes
		}
	}
	norm := func(pos fpl.Position) float64 {
		if posMinutes[pos] == 0 {
			return 0
		}
		return posPoints[pos] / (posMinutes[pos] / 90)
	}

	m := &Model{params: p, next: next, fixtures: fixtures, perGame: make(map[int]float64, len(players))}
	for _, pl := range players {
		s := byPlayer[pl.ID]
		if s == nil || s.rateMinutes == 0 {
			m.perGame[pl.ID] = 0 // has not played: nothing to expect
			continue
		}
		games := s.rateMinutes / 90 // weighted full games played
		own := s.points / games
		rate := (games*own + p.PriorGames*norm(pl.Position)) / (games + p.PriorGames)
		share := s.shareMinutes / (90 * minTotal) // typical fraction of a match played per gameweek
		m.perGame[pl.ID] = rate * share
	}
	return m, nil
}

// Expected is the points a player is expected to score in one gameweek: his
// expected points per match times the difficulty multiplier of each match he
// plays in it. A blank gameweek gives 0 and a double gives both matches.
func (m *Model) Expected(p fpl.Player, gameweek int) float64 {
	base, ok := m.perGame[p.ID]
	if !ok || base == 0 {
		return 0
	}
	difficulties := []int{3}
	if len(m.fixtures) > 0 {
		difficulties = m.fixtures.Difficulties(p.TeamID, gameweek)
	}
	var total float64
	for _, d := range difficulties {
		total += base * m.multiplier(d)
	}
	return total
}

func (m *Model) multiplier(difficulty int) float64 {
	d := min(max(difficulty, 1), 5)
	return math.Max(minMultiplier, 1+m.params.FixtureSlope*float64(3-d))
}

// AsOf is the first gameweek with no history: where a model fed this data should
// start predicting from. It is not necessarily the gameweek being decided for: while
// a gameweek is under way its deadline has passed but its history is incomplete, so
// the next deadline is later than AsOf. Use ScoreFrom for that.
func AsOf(history []fpl.PlayerHistory) int {
	last := 1
	for _, h := range history {
		last = max(last, h.GameweekID)
	}
	return last + 1
}

// Score is ScoreFrom the first gameweek the model has no data for.
func (m *Model) Score(horizon int) func(fpl.Player) float64 { return m.ScoreFrom(m.next, horizon) }

// ScoreFrom returns a function giving each player's expected points per gameweek
// averaged over horizon gameweeks starting at start, discounted by his chance of
// playing. It is the figure the transfer search and squad builder rank by. start is
// the gameweek being decided for, which can be later than the model's history ends.
func (m *Model) ScoreFrom(start, horizon int) func(fpl.Player) float64 {
	horizon = max(horizon, 1)
	return func(p fpl.Player) float64 {
		var total float64
		for g := start; g < start+horizon; g++ {
			total += m.Expected(p, g)
		}
		return total / float64(horizon) * optimizer.AvailabilityFactor(p)
	}
}

// HistoryNote says so when stored history is behind the season: asOf is the first
// gameweek with no data (see AsOf) and lastFinished the latest gameweek FPL says is
// over. Form and expected points are then a gameweek stale, which a manager should
// know before trusting them. It returns "" when the data is current.
func HistoryNote(asOf, lastFinished int) string {
	if lastFinished == 0 || lastFinished < asOf {
		return ""
	}
	return fmt.Sprintf("Stored player history ends at GW%d but GW%d has finished, so form and expected points are behind: run `gaffer sync`.", asOf-1, lastFinished)
}
