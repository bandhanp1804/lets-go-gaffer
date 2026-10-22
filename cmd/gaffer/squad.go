package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// squadHorizon is how many gameweeks ahead the squad builder looks.
const squadHorizon = 4

// runSquad builds the best squad from scratch under the £100.0m budget and the
// FPL rules, ranking players by expected points over the next few gameweeks.
func runSquad(ctx context.Context) error {
	dsn := os.Getenv("GAFFER_DSN")
	if dsn == "" {
		return fmt.Errorf("GAFFER_DSN is not set")
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
	teams, err := store.ListTeams(ctx)
	if err != nil {
		return err
	}
	history, err := store.ListHistory(ctx)
	if err != nil {
		return err
	}
	if len(players) == 0 || len(history) == 0 {
		return fmt.Errorf("no players or history stored; run `gaffer sync` first")
	}
	fixtures, err := fpl.NewHTTPClient(politeClient(100 * time.Millisecond)).FetchFixtures(ctx)
	if err != nil {
		return err
	}
	next := 1
	for _, h := range history {
		next = max(next, h.GameweekID+1)
	}
	model, err := projection.New(players, history, fixtures, projection.Default, next)
	if err != nil {
		return err
	}
	score := model.Score(squadHorizon)

	var cands []optimizer.Candidate
	for _, p := range players {
		if optimizer.Available(p) {
			cands = append(cands, optimizer.Candidate{Player: p, Cost: p.NowCost, Score: score(p)})
		}
	}
	best, err := optimizer.BestSquad(cands, optimizer.Budget)
	if err != nil {
		return fmt.Errorf("build squad: %w", err)
	}

	fmt.Printf("Best squad for GW%d (expected points over the next %d gameweeks)\n\n", next, squadHorizon)
	printSquad(os.Stdout, best.Squad, teams)
	fmt.Printf("\nExpected points per gameweek: %.1f", best.Value)
	if greedy, gerr := optimizer.Greedy(players, optimizer.Options{Score: score}); gerr == nil {
		g := optimizer.TeamValue(greedy.Players, score)
		fmt.Printf("  (the greedy baseline finds %.1f: exact is %+.1f better)", g, best.Value-g)
	}
	fmt.Println()
	return nil
}

// formatPrice renders tenths of millions as £x.ym using integers only.
func formatPrice(tenths int) string {
	return fmt.Sprintf("£%d.%dm", tenths/10, tenths%10)
}

func printSquad(w io.Writer, s fpl.Squad, teams []fpl.Team) {
	printSquadTable(w, s, teams)
	total := 0
	for _, p := range s.Players {
		total += p.NowCost
	}
	fmt.Fprintf(w, "\nSquad cost %s of %s\n", formatPrice(total), formatPrice(optimizer.Budget))
}

// printSquadTable prints the starting XI and bench with captaincy markers.
func printSquadTable(w io.Writer, s fpl.Squad, teams []fpl.Team) {
	short := make(map[int]string, len(teams))
	for _, t := range teams {
		short[t.ID] = t.ShortName
	}
	starting := make(map[int]bool, len(s.Starting))
	for _, id := range s.Starting {
		starting[id] = true
	}

	fmt.Fprintf(w, "%-4s %-16s %-4s %7s %6s  %s\n", "POS", "PLAYER", "TEAM", "PRICE", "FORM", "")
	for _, section := range []struct {
		title string
		in    bool
	}{{"Starting XI", true}, {"Bench", false}} {
		fmt.Fprintf(w, "-- %s --\n", section.title)
		for _, p := range s.Players {
			if starting[p.ID] != section.in {
				continue
			}
			role := ""
			switch p.ID {
			case s.Captain:
				role = "(C)"
			case s.ViceCaptain:
				role = "(VC)"
			}
			fmt.Fprintf(w, "%-4s %-16s %-4s %7s %6s  %s\n",
				p.Position, p.WebName, short[p.TeamID], formatPrice(p.NowCost), p.Form, role)
		}
	}
}
