package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// runBacktest measures how well each way of scoring players ranked them for
// gameweeks that have already been played, using only the gameweeks before each.
func runBacktest(ctx context.Context) error {
	dsn := os.Getenv("GAFFER_DSN")
	if dsn == "" {
		return errors.New("GAFFER_DSN is not set")
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	store := db.NewStore(pool)
	players, err := store.ListPlayers(ctx)
	if err != nil {
		return err
	}
	history, err := store.ListHistory(ctx)
	if err != nil {
		return err
	}
	if len(history) == 0 {
		return errors.New("no history stored; run `gaffer sync` first")
	}
	fixtures, err := fpl.NewHTTPClient(politeClient(100 * time.Millisecond)).FetchFixtures(ctx)
	if err != nil {
		return err
	}

	last := 0
	for _, h := range history {
		last = max(last, h.GameweekID)
	}
	const first = 3 // two earlier gameweeks is the least that says anything
	if last < first {
		return fmt.Errorf("need at least %d gameweeks of history, have %d", first, last)
	}

	model := func(p projection.Params) func(next int) func(fpl.Player) float64 {
		return func(next int) func(fpl.Player) float64 {
			m, err := projection.New(players, history, fixtures, p, next)
			if err != nil {
				return func(fpl.Player) float64 { return 0 }
			}
			// Availability is left out: today's injury list cannot be applied to past weeks.
			return func(pl fpl.Player) float64 { return m.Expected(pl, next) }
		}
	}
	eval := func(f func(int) func(fpl.Player) float64) projection.Metrics {
		return projection.Evaluate(players, history, first, last, f)
	}
	form := func(next int) func(fpl.Player) float64 { return projection.FormBaseline(history, next) }
	season := func(next int) func(fpl.Player) float64 { return projection.SeasonBaseline(history, next) }

	fmt.Printf("Backtest: predicting gameweeks %d-%d, each from only the gameweeks before it.\n", first, last)
	fmt.Println("Spearman: rank correlation with actual points (higher is better). Top-20: average actual points of the 20 players ranked highest.")
	fmt.Println()
	fmt.Printf("%-44s %9s %7s %8s\n", "MODEL", "SPEARMAN", "MAE", "TOP-20")
	row := func(name string, m projection.Metrics) {
		fmt.Printf("%-44s %9.3f %7.2f %8.2f\n", name, m.Spearman, m.MAE, m.TopMean)
	}
	row("form: last 3 games (the old scorer)", eval(form))
	row("season average points per gameweek", eval(season))
	row("model: season average (no decay/prior/fixtures)", eval(model(projection.Params{Decay: 1})))
	row("model: + recency decay 0.8", eval(model(projection.Params{Decay: 0.8})))
	row("model: long-memory skill, short-memory minutes", eval(model(projection.Params{Decay: 1, MinutesDecay: 0.35})))
	row("model: + shrink toward position norm (3 games)", eval(model(projection.Params{Decay: 0.8, PriorGames: 3})))
	row("model: + fixture difficulty (default)", eval(model(projection.Default)))

	fmt.Println()
	fmt.Println("Per gameweek, old scorer vs default model (Spearman):")
	wins := 0
	for g := first; g <= last; g++ {
		o := projection.Evaluate(players, history, g, g, form)
		n := projection.Evaluate(players, history, g, g, model(projection.Default))
		if n.Spearman > o.Spearman {
			wins++
		}
		fmt.Printf("  GW%-2d  old %6.3f   model %6.3f\n", g, o.Spearman, n.Spearman)
	}
	fmt.Printf("  the model wins %d of %d gameweeks\n", wins, last-first+1)

	type cfg struct {
		p projection.Params
		m projection.Metrics
	}
	var grid []cfg
	for _, decay := range []float64{0.7, 0.85, 1} {
		for _, minutes := range []float64{0.2, 0.35, 0.5, 0.7} {
			for _, prior := range []float64{1, 3, 5, 8} {
				for _, slope := range []float64{0.1, 0.15, 0.2} {
					p := projection.Params{Decay: decay, MinutesDecay: minutes, PriorGames: prior, FixtureSlope: slope}
					grid = append(grid, cfg{p, eval(model(p))})
				}
			}
		}
	}
	sort.SliceStable(grid, func(i, j int) bool { return grid[i].m.Spearman > grid[j].m.Spearman })
	fmt.Println()
	fmt.Println("Best settings found (rate decay, minutes decay, prior games, fixture slope). Treat the top few as a region, not a point:")
	for _, c := range grid[:8] {
		fmt.Printf("  rate %.2f  minutes %.2f  prior %.0f  slope %.2f   spearman %.3f  mae %.2f  top-20 %.2f\n",
			c.p.Decay, c.p.MinutesDecay, c.p.PriorGames, c.p.FixtureSlope, c.m.Spearman, c.m.MAE, c.m.TopMean)
	}

	// Walk-forward: tune using only gameweeks before k, then judge on k. This is the
	// honest test of whether tuning generalises, because the tuned settings never
	// saw the week they are scored on.
	fmt.Println()
	fmt.Println("Walk-forward check (tune on earlier gameweeks, score the next one it never saw):")
	var tunedSum, fixedSum, oldSum float64
	n := 0
	for k := first + 2; k <= last; k++ {
		best, bestScore := projection.Default, -2.0
		for _, c := range grid {
			m := projection.Evaluate(players, history, first, k-1, model(c.p))
			if m.Spearman > bestScore {
				best, bestScore = c.p, m.Spearman
			}
		}
		tuned := projection.Evaluate(players, history, k, k, model(best)).Spearman
		fixed := projection.Evaluate(players, history, k, k, model(projection.Default)).Spearman
		old := projection.Evaluate(players, history, k, k, form).Spearman
		fmt.Printf("  GW%-2d tuned on GW%d-%d -> rate %.2f minutes %.2f prior %.0f slope %.2f   tuned %6.3f   default %6.3f   old %6.3f\n",
			k, first, k-1, best.Decay, best.MinutesDecay, best.PriorGames, best.FixtureSlope, tuned, fixed, old)
		tunedSum += tuned
		fixedSum += fixed
		oldSum += old
		n++
	}
	if n > 0 {
		fmt.Printf("  average out of sample:  tuned %.3f   default %.3f   old scorer %.3f\n",
			tunedSum/float64(n), fixedSum/float64(n), oldSum/float64(n))
	}
	return nil
}
