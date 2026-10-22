package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

const transfersUsage = `usage: gaffer transfers [-team ID] [-free N] [-wildcard] [-horizon N] [-max N] [-n N] [-prices]
  -team      your FPL team id (default GAFFER_TEAM_ID)
  -free      free transfers this gameweek (default: replayed from your history)
  -wildcard  plan a Wildcard (or Free Hit): unlimited transfers, no points hit
  -horizon   gameweeks a gain is assumed to last (default 4)
  -max       most players to change in a normal plan (default 2)
  -n         suggestions to show (default 5)
  -prices    also print the selling price of each of your players
` + swapHelp

// transfersView is everything printTransfers needs.
type transfersView struct {
	Team        myteam.Team
	Teams       []fpl.Team
	Fin         myteam.Finances
	Free        int    // free transfers used for the search
	FreeSource  string // where Free came from
	Horizon     int
	Wildcard    bool
	ShowPrices  bool
	Suggestions []transfers.Suggestion
	Pending     string // transfers the manager entered with -swap, if any
}

// runTransfers suggests this week's best transfers for the manager's real squad.
func runTransfers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("transfers", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	teamFlag := fs.String("team", os.Getenv("GAFFER_TEAM_ID"), "")
	free := fs.Int("free", -1, "")
	wildcard := fs.Bool("wildcard", false, "")
	horizon := fs.Int("horizon", 4, "")
	maxMoves := fs.Int("max", 2, "")
	topN := fs.Int("n", 5, "")
	prices := fs.Bool("prices", false, "")
	var swaps stringList
	fs.Var(&swaps, "swap", "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, transfersUsage)
	}
	if fs.NArg() != 0 || *horizon < 1 || *maxMoves < 1 || *topN < 1 || *free < -1 {
		return errors.New(transfersUsage)
	}
	teamID, err := strconv.Atoi(strings.TrimSpace(*teamFlag))
	if err != nil || teamID < 1 {
		return fmt.Errorf("a team id is needed: set GAFFER_TEAM_ID or pass -team\n%s", transfersUsage)
	}
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
	if len(players) == 0 {
		return errors.New("no players stored; run `gaffer sync` first")
	}
	teams, err := store.ListTeams(ctx)
	if err != nil {
		return err
	}

	client := fpl.NewHTTPClient(politeClient(100 * time.Millisecond))
	team, err := myteam.Load(ctx, client, teamID, players)
	if err != nil {
		return err
	}
	ids := make([]int, len(team.Squad.Players))
	for i, p := range team.Squad.Players {
		ids[i] = p.ID
	}
	start, err := store.StartingPrices(ctx, ids)
	if err != nil {
		return err
	}
	fin, err := myteam.LoadFinances(ctx, client, teamID, team, start)
	if err != nil {
		return fmt.Errorf("%w (run `gaffer sync` so starting prices are stored)", err)
	}

	// Transfers already made this week change the squad, the bank, and the free transfers left.
	team, changes, err := applyPending(os.Stdout, team, players, swaps)
	if err != nil {
		return err
	}
	fin = myteam.ApplyFinances(fin, changes)

	// Rank by expected points over the horizon, not raw form (see `gaffer backtest`).
	history, err := store.ListHistory(ctx)
	if err != nil {
		return err
	}
	fixtures, err := client.FetchFixtures(ctx)
	if err != nil {
		return err
	}
	model, err := projection.New(players, history, fixtures, projection.Default, team.Event+1)
	if err != nil {
		return err
	}

	view := transfersView{Team: team, Teams: teams, Fin: fin, Horizon: *horizon, Wildcard: *wildcard, ShowPrices: *prices}
	view.Pending = myteam.DescribeChanges(changes)
	view.Free, view.FreeSource = fin.Free.Next, "replayed from your history"
	if len(changes) > 0 {
		view.FreeSource = fmt.Sprintf("replayed from your history, %d already used by your -swap", len(changes))
	}
	if *free >= 0 {
		view.Free, view.FreeSource = *free, "set by -free"
	}

	in := transfers.Input{Squad: team.Squad.Players, Sell: fin.Sell, Bank: fin.Bank, Pool: players}
	opts := transfers.Options{
		Score: model.Score(*horizon), FreeTransfers: view.Free, MaxTransfers: *maxMoves,
		Horizon: *horizon, TopN: *topN,
	}
	if *wildcard {
		w, err := transfers.Wildcard(in, opts)
		if err != nil {
			return err
		}
		if w.Transfers > 0 {
			view.Suggestions = []transfers.Suggestion{w}
		}
	} else if view.Suggestions, err = transfers.Suggest(in, opts); err != nil {
		return err
	}
	printTransfers(os.Stdout, view)
	return nil
}

func printTransfers(w io.Writer, v transfersView) {
	short := make(map[int]string, len(v.Teams))
	for _, t := range v.Teams {
		short[t.ID] = t.ShortName
	}

	fmt.Fprintf(w, "%s: transfers for GW%d\n", v.Team.Entry.Name, v.Team.Event+1)
	if v.Wildcard {
		fmt.Fprintf(w, "Wildcard: unlimited transfers, no hit · bank %s\n", formatPrice(v.Fin.Bank))
	} else {
		fmt.Fprintf(w, "Free transfers: %d (%s) · bank %s · horizon %d gameweeks\n",
			v.Free, v.FreeSource, formatPrice(v.Fin.Bank), v.Horizon)
	}
	fmt.Fprintln(w, "Score: expected points per gameweek (season record weighted to recent form, shrunk toward the position norm,")
	fmt.Fprintln(w, "scaled by fixture difficulty and the chance of playing)")
	fmt.Fprintln(w, "xP is expected points per gameweek over the horizon: the number moves are ranked by, which can differ a lot from form.")
	fmt.Fprintln(w, "Selling prices are computed from FPL's rule (you keep half of any price rise, rounded down);")
	fmt.Fprintln(w, "check them on the FPL transfers page before relying on a move.")
	if v.Pending != "" {
		fmt.Fprintf(w, "You have already made %d transfers this week (%s), costing -%d points so far.\n",
			v.Fin.PendingTransfers, v.Pending, v.Fin.PendingHit)
	}
	if m := v.Fin.Free.Mismatches; len(m) > 0 {
		parts := make([]string, len(m))
		for i, gw := range m {
			parts[i] = strconv.Itoa(gw)
		}
		fmt.Fprintf(w, "WARNING: the free-transfer count could not be verified against your history (GW %s disagree).\n"+
			"Rerun with -free N to set it yourself.\n", strings.Join(parts, ", "))
	}
	fmt.Fprintln(w)

	if len(v.Suggestions) == 0 {
		if v.Wildcard {
			fmt.Fprintln(w, "The wildcard finds nothing better than your current squad.")
		} else {
			fmt.Fprintf(w, "No transfer beats keeping your squad over %d gameweeks after the cost of hits.\n", v.Horizon)
			fmt.Fprintf(w, "Rolling your %s leaves you with %s next week.\n",
				freeTransfers(v.Free), freeTransfers(min(v.Free+1, transfers.MaxBankedFreeTransfers)))
		}
	}
	for i, s := range v.Suggestions {
		hit := "no hit"
		if s.Hit > 0 {
			hit = fmt.Sprintf("-%d hit", s.Hit)
		}
		noun := "transfers"
		if s.Transfers == 1 {
			noun = "transfer"
		}
		if v.Wildcard {
			fmt.Fprintf(w, "Wildcard rebuild: %d %s, %s · +%.1f a gameweek\n", s.Transfers, noun, hit, s.ScoreGain)
		} else {
			fmt.Fprintf(w, "%d. %+.1f net · %d %s, %s · %+.1f a gameweek over %d\n",
				i+1, s.Net, s.Transfers, noun, hit, s.ScoreGain, v.Horizon)
		}
		for _, m := range s.Moves {
			fmt.Fprintf(w, "   OUT %-13s %-4s %-3s sells %-7s xP %4.1f  (form %s)\n",
				m.Out.WebName, short[m.Out.TeamID], m.Out.Position, formatPrice(m.SellPrice), m.OutScore, m.Out.Form)
			fmt.Fprintf(w, "   IN  %-13s %-4s %-3s costs %-7s xP %4.1f  (form %s)\n",
				m.In.WebName, short[m.In.TeamID], m.In.Position, formatPrice(m.In.NowCost), m.InScore, m.In.Form)
		}
		fmt.Fprintf(w, "   bank after %s\n\n", formatPrice(s.BankAfter))
	}

	if v.ShowPrices {
		fmt.Fprintln(w, "Selling prices (computed)")
		fmt.Fprintf(w, "%-4s %-14s %-4s %8s %8s %8s\n", "POS", "PLAYER", "TEAM", "BOUGHT", "NOW", "SELLS")
		for _, p := range v.Team.Squad.Players {
			fmt.Fprintf(w, "%-4s %-14s %-4s %8s %8s %8s\n", p.Position, p.WebName, short[p.TeamID],
				formatPrice(v.Fin.Buy[p.ID]), formatPrice(p.NowCost), formatPrice(v.Fin.Sell[p.ID]))
		}
	}
}

func freeTransfers(n int) string {
	if n == 1 {
		return "1 free transfer"
	}
	return fmt.Sprintf("%d free transfers", n)
}
