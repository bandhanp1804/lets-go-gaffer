package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	rep, err := projection.RunBacktest(ctx, players, history, fixtures)
	if err != nil {
		return err
	}
	printBacktest(rep)
	return nil
}

func printBacktest(rep projection.BacktestReport) {
	fmt.Printf("Backtest: predicting gameweeks %d-%d, each from only the gameweeks before it.\n", rep.First, rep.Last)
	fmt.Println("Spearman: rank correlation with actual points (higher is better). Top-20: average actual points of the 20 players ranked highest.")
	fmt.Println()
	fmt.Printf("%-44s %9s %7s %8s\n", "MODEL", "SPEARMAN", "MAE", "TOP-20")
	for _, m := range rep.Models {
		fmt.Printf("%-44s %9.3f %7.2f %8.2f\n", m.Name, m.Spearman, m.MAE, m.TopMean)
	}

	fmt.Println()
	fmt.Println("Per gameweek, old scorer vs default model (Spearman):")
	for _, g := range rep.PerGameweek {
		fmt.Printf("  GW%-2d  old %6.3f   model %6.3f\n", g.Gameweek, g.Old, g.Model)
	}
	fmt.Printf("  the model wins %d of %d gameweeks\n", rep.ModelWins, len(rep.PerGameweek))

	fmt.Println()
	fmt.Println("Best settings found (rate decay, minutes decay, prior games, fixture slope). Treat the top few as a region, not a point:")
	for _, c := range rep.Best {
		fmt.Printf("  rate %.2f  minutes %.2f  prior %.0f  slope %.2f   spearman %.3f  mae %.2f  top-20 %.2f\n",
			c.Params.Decay, c.Params.MinutesDecay, c.Params.PriorGames, c.Params.FixtureSlope, c.Spearman, c.MAE, c.TopMean)
	}

	fmt.Println()
	fmt.Println("Walk-forward check (tune on earlier gameweeks, score the next one it never saw):")
	for _, w := range rep.WalkForward {
		fmt.Printf("  GW%-2d tuned on GW%d-%d -> rate %.2f minutes %.2f prior %.0f slope %.2f   tuned %6.3f   default %6.3f   old %6.3f\n",
			w.Gameweek, w.TunedFrom, w.TunedTo, w.Params.Decay, w.Params.MinutesDecay, w.Params.PriorGames, w.Params.FixtureSlope, w.Tuned, w.Default, w.OldRho)
	}
	if len(rep.WalkForward) > 0 {
		fmt.Printf("  average out of sample:  tuned %.3f   default %.3f   old scorer %.3f\n", rep.AvgTuned, rep.AvgDefault, rep.AvgOld)
	}
}
