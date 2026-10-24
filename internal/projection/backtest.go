package projection

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// BacktestFirst is the earliest gameweek judged: two earlier gameweeks is the least
// that says anything.
const BacktestFirst = 3

// ErrTooLittleHistory means there are not enough played gameweeks to backtest.
var ErrTooLittleHistory = errors.New("not enough gameweeks of history to backtest")

// ModelRow is one way of scoring players and how well it ranked past gameweeks.
type ModelRow struct {
	Name string
	Metrics
}

// GameweekRow compares the old scorer (recent form) with the default model for one gameweek.
type GameweekRow struct {
	Gameweek   int
	Old, Model float64 // Spearman
}

// GridRow is one parameter setting and its score over every judged gameweek.
type GridRow struct {
	Params Params
	Metrics
}

// WalkRow is one step of the walk-forward check: settings tuned on the gameweeks
// before Gameweek, then scored on Gameweek, which they never saw.
type WalkRow struct {
	Gameweek               int
	TunedFrom, TunedTo     int
	Params                 Params
	Tuned, Default, OldRho float64 // Spearman on Gameweek
}

// BacktestReport is everything `gaffer backtest` and the API show.
type BacktestReport struct {
	First, Last int
	Models      []ModelRow
	PerGameweek []GameweekRow
	ModelWins   int       // gameweeks where the default model beats the old scorer
	Best        []GridRow // the best settings found, best first
	WalkForward []WalkRow
	// Averages of the walk-forward Spearman; zero when there were no steps.
	AvgTuned, AvgDefault, AvgOld float64
}

// RunBacktest measures how well each way of scoring players ranked gameweeks that have
// already been played, using only the gameweeks before each. It stops early when ctx is
// cancelled.
func RunBacktest(ctx context.Context, players []fpl.Player, history []fpl.PlayerHistory, fixtures fpl.Fixtures) (BacktestReport, error) {
	last := 0
	for _, h := range history {
		last = max(last, h.GameweekID)
	}
	first := BacktestFirst
	if last < first {
		return BacktestReport{}, fmt.Errorf("%w: need %d gameweeks, have %d", ErrTooLittleHistory, first, last)
	}
	rep := BacktestReport{First: first, Last: last}

	model := func(p Params) func(next int) func(fpl.Player) float64 {
		return func(next int) func(fpl.Player) float64 {
			m, err := New(players, history, fixtures, p, next)
			if err != nil {
				return func(fpl.Player) float64 { return 0 }
			}
			// Availability is left out: today's injury list cannot be applied to past weeks.
			return func(pl fpl.Player) float64 { return m.Expected(pl, next) }
		}
	}
	eval := func(f func(int) func(fpl.Player) float64) Metrics { return Evaluate(players, history, first, last, f) }
	form := func(next int) func(fpl.Player) float64 { return FormBaseline(history, next) }
	season := func(next int) func(fpl.Player) float64 { return SeasonBaseline(history, next) }

	for _, m := range []struct {
		name string
		f    func(int) func(fpl.Player) float64
	}{
		{"form: last 3 games (the old scorer)", form},
		{"season average points per gameweek", season},
		{"model: season average (no decay/prior/fixtures)", model(Params{Decay: 1})},
		{"model: + recency decay 0.8", model(Params{Decay: 0.8})},
		{"model: long-memory skill, short-memory minutes", model(Params{Decay: 1, MinutesDecay: 0.35})},
		{"model: + shrink toward position norm (3 games)", model(Params{Decay: 0.8, PriorGames: 3})},
		{"model: + fixture difficulty (default)", model(Default)},
	} {
		rep.Models = append(rep.Models, ModelRow{Name: m.name, Metrics: eval(m.f)})
	}

	for g := first; g <= last; g++ {
		o := Evaluate(players, history, g, g, form).Spearman
		n := Evaluate(players, history, g, g, model(Default)).Spearman
		if n > o {
			rep.ModelWins++
		}
		rep.PerGameweek = append(rep.PerGameweek, GameweekRow{Gameweek: g, Old: o, Model: n})
	}

	var grid []GridRow
	for _, decay := range []float64{0.7, 0.85, 1} {
		for _, minutes := range []float64{0.2, 0.35, 0.5, 0.7} {
			for _, prior := range []float64{1, 3, 5, 8} {
				for _, slope := range []float64{0.1, 0.15, 0.2} {
					if err := ctx.Err(); err != nil {
						return BacktestReport{}, err
					}
					p := Params{Decay: decay, MinutesDecay: minutes, PriorGames: prior, FixtureSlope: slope}
					grid = append(grid, GridRow{Params: p, Metrics: eval(model(p))})
				}
			}
		}
	}
	sort.SliceStable(grid, func(i, j int) bool { return grid[i].Spearman > grid[j].Spearman })
	rep.Best = grid[:min(8, len(grid))]

	// Walk-forward: tune using only gameweeks before k, then judge on k. This is the
	// honest test of whether tuning generalises, because the tuned settings never
	// saw the week they are scored on.
	for k := first + 2; k <= last; k++ {
		if err := ctx.Err(); err != nil {
			return BacktestReport{}, err
		}
		best, bestScore := Default, -2.0
		for _, c := range grid {
			m := Evaluate(players, history, first, k-1, model(c.Params))
			if m.Spearman > bestScore {
				best, bestScore = c.Params, m.Spearman
			}
		}
		row := WalkRow{
			Gameweek: k, TunedFrom: first, TunedTo: k - 1, Params: best,
			Tuned:   Evaluate(players, history, k, k, model(best)).Spearman,
			Default: Evaluate(players, history, k, k, model(Default)).Spearman,
			OldRho:  Evaluate(players, history, k, k, form).Spearman,
		}
		rep.WalkForward = append(rep.WalkForward, row)
		rep.AvgTuned += row.Tuned
		rep.AvgDefault += row.Default
		rep.AvgOld += row.OldRho
	}
	if n := float64(len(rep.WalkForward)); n > 0 {
		rep.AvgTuned, rep.AvgDefault, rep.AvgOld = rep.AvgTuned/n, rep.AvgDefault/n, rep.AvgOld/n
	}
	return rep, nil
}
