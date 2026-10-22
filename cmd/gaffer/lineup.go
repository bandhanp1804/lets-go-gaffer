package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// captainTossUp is the expected-points gap under which the captain choice is a coin flip.
const captainTossUp = 0.3

const lineupUsage = `usage: gaffer lineup [TEAM_ID]
  TEAM_ID defaults to GAFFER_TEAM_ID from .env`

// lineupView is everything printLineup needs.
type lineupView struct {
	Team      myteam.Team
	Teams     []fpl.Team
	Next      int // the gameweek being picked for
	Advice    lineup.Advice
	Opponents map[int][]fpl.Opponent // player id -> that player's matches in Next
}

// runLineup recommends the best XI from the manager's real squad for the
// upcoming gameweek, ranking players by expected points in that gameweek alone.
func runLineup(ctx context.Context, args []string) error {
	raw := os.Getenv("GAFFER_TEAM_ID")
	if len(args) == 1 {
		raw = args[0]
	}
	if len(args) > 1 || raw == "" {
		return errors.New(lineupUsage)
	}
	teamID, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || teamID < 1 {
		return fmt.Errorf("invalid team id %q\n%s", raw, lineupUsage)
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
	teams, err := store.ListTeams(ctx)
	if err != nil {
		return err
	}
	history, err := store.ListHistory(ctx)
	if err != nil {
		return err
	}
	if len(players) == 0 || len(history) == 0 {
		return errors.New("no players or history stored; run `gaffer sync` first")
	}

	client := fpl.NewHTTPClient(politeClient(100 * time.Millisecond))
	team, err := myteam.Load(ctx, client, teamID, players)
	if err != nil {
		return err
	}
	fixtures, err := client.FetchFixtures(ctx)
	if err != nil {
		return err
	}
	next := team.Event + 1
	model, err := projection.New(players, history, fixtures, projection.Default, next)
	if err != nil {
		return err
	}

	// A lineup is for one match week, so rank by expected points in that gameweek only.
	advice, err := lineup.Advise(team.Squad.Players, lineup.Options{XP: model.Score(1)})
	if err != nil {
		return err
	}
	opponents := make(map[int][]fpl.Opponent, len(team.Squad.Players))
	for _, p := range team.Squad.Players {
		opponents[p.ID] = fixtures.Opponents(p.TeamID, next)
	}
	printLineup(os.Stdout, lineupView{Team: team, Teams: teams, Next: next, Advice: advice, Opponents: opponents})
	return nil
}

func printLineup(w io.Writer, v lineupView) {
	short := make(map[int]string, len(v.Teams))
	for _, t := range v.Teams {
		short[t.ID] = t.ShortName
	}
	a := v.Advice

	fmt.Fprintf(w, "%s: best XI for GW%d\n", v.Team.Entry.Name, v.Next)
	fmt.Fprintf(w, "Formation %s · %.1f expected points (the captain counts twice) · xP = expected points in GW%d only\n\n", a.Formation, a.Total, v.Next)

	row := func(e lineup.Entry) {
		role := ""
		switch e.Player.ID {
		case a.Captain.Player.ID:
			role = " (C)"
		case a.ViceCaptain.Player.ID:
			role = " (VC)"
		}
		note := ""
		if f := availabilityNote(e.Player); f != "" {
			note = "  ! " + f
		}
		fmt.Fprintf(w, " %-3s %-13s %-4s %-24s xP %4.1f%s%s\n",
			e.Player.Position, e.Player.WebName, short[e.Player.TeamID], formatOpponents(v.Opponents[e.Player.ID], short), e.XP, role, note)
	}
	fmt.Fprintln(w, "Starting XI")
	for _, e := range a.Starting {
		row(e)
	}
	fmt.Fprintln(w, "\nBench (in auto-sub order)")
	for i, e := range a.Bench {
		fmt.Fprintf(w, " %d", i+1)
		row(e)
	}

	// The captain's points count twice, so a near tie here matters more than any bench call.
	lead := a.Captain.XP - a.ViceCaptain.XP
	if lead < captainTossUp {
		fmt.Fprintf(w, "\nCaptain %s or %s is a toss-up for the captain's armband (gap %.2f xP): break it with fixture, form, or instinct.\n",
			a.Captain.Player.WebName, a.ViceCaptain.Player.WebName, lead)
	} else {
		fmt.Fprintf(w, "\nCaptain %s, vice %s: the captain leads by %.1f xP.\n",
			a.Captain.Player.WebName, a.ViceCaptain.Player.WebName, lead)
	}

	fmt.Fprintln(w)
	if len(a.CloseCalls) == 0 {
		fmt.Fprintln(w, "No close calls: nobody on the bench is within reach of a start.")
	} else {
		fmt.Fprintln(w, "Close calls (a bench player this near is a toss-up)")
		for _, c := range a.CloseCalls {
			fmt.Fprintf(w, "  %s would start in place of %s with just %.1f more xP\n",
				c.Bench.Player.WebName, c.Displaces.Player.WebName, c.Margin)
		}
	}

	var others []string
	for _, f := range a.Formations {
		if f.Formation != a.Formation && len(others) < 4 {
			others = append(others, fmt.Sprintf("%s (%+.1f)", f.Formation, f.Delta))
		}
	}
	if len(others) > 0 {
		fmt.Fprintf(w, "\nOther formations: %s\n", strings.Join(others, ", "))
	}

	// What changed since the lineup set at the last deadline. That is the GW before
	// this one; changes made since then need a login and are not visible.
	names := map[int]string{}
	var now []int
	for _, e := range a.Starting {
		names[e.Player.ID] = e.Player.WebName
		now = append(now, e.Player.ID)
	}
	for _, e := range a.Bench {
		names[e.Player.ID] = e.Player.WebName
	}
	in, out := lineup.Changes(v.Team.Squad.Starting, now)
	fmt.Fprintln(w)
	if len(in) == 0 && len(out) == 0 {
		fmt.Fprintf(w, "This is the same XI as your GW%d lineup.\n", v.Team.Event)
		return
	}
	fmt.Fprintf(w, "Changes from your GW%d lineup:\n", v.Team.Event)
	fmt.Fprintf(w, "  IN  %s\n  OUT %s\n", joinNames(in, names), joinNames(out, names))
}

// formatOpponents renders a player's matches, e.g. "MCI (H) d2" or, for a
// double gameweek, "ARS (H) d2 + MCI (A) d5". d is FPL's 1-5 difficulty.
func formatOpponents(opps []fpl.Opponent, short map[int]string) string {
	if len(opps) == 0 {
		return "no fixture"
	}
	parts := make([]string, len(opps))
	for i, o := range opps {
		venue, name := "A", short[o.Team]
		if o.Home {
			venue = "H"
		}
		if name == "" {
			name = "?"
		}
		parts[i] = fmt.Sprintf("%s (%s) d%d", name, venue, o.Difficulty)
	}
	return strings.Join(parts, " + ")
}

// availabilityNote words a player's injury or doubt for the lineup screen.
func availabilityNote(p fpl.Player) string {
	chance := p.ChanceOfPlayingNextRound
	if p.Status == "a" && p.News == "" && (chance == nil || *chance >= 100) {
		return ""
	}
	label := statusLabel(p.Status)
	if chance != nil && *chance < 100 {
		label += fmt.Sprintf(", %d%%", *chance)
	}
	return label
}

func joinNames(ids []int, names map[int]string) string {
	if len(ids) == 0 {
		return "(none)"
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if out[i] = names[id]; out[i] == "" {
			out[i] = fmt.Sprintf("player %d", id)
		}
	}
	return strings.Join(out, ", ")
}
