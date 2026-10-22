package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

const teamUsage = `usage: gaffer team [-swap Out>In]... [TEAM_ID]
  TEAM_ID defaults to GAFFER_TEAM_ID from .env. Find yours in the address bar when
  you open the Points page on the FPL site: .../entry/<TEAM_ID>/event/<N>
` + swapHelp

// runTeam pulls a manager's public squad and shows it with availability flags.
// It reads only public data, so no FPL login is needed or used.
func runTeam(ctx context.Context, args []string) error {
	teamID, swaps, err := parseTeamArgs("team", teamUsage, args)
	if err != nil {
		return err
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
		var se *httpx.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return fmt.Errorf("FPL has no visible squad for team %d (wrong ID, or the gameweek's deadline has not passed): %w", teamID, err)
		}
		return err
	}
	team, _, err = applyPending(os.Stdout, team, players, swaps)
	if err != nil {
		return err
	}
	printTeam(os.Stdout, team, teams)
	return nil
}

func printTeam(w io.Writer, team myteam.Team, teams []fpl.Team) {
	h := team.History
	fmt.Fprintf(w, "%s: %s %s\n", team.Entry.Name, team.Entry.FirstName, team.Entry.LastName)
	line := fmt.Sprintf("GW%d · %d pts · overall rank %s · bank %s · value %s",
		team.Event, h.Points, commas(h.OverallRank), formatPrice(h.Bank), formatPrice(h.Value))
	if h.EventTransfers > 0 {
		line += fmt.Sprintf(" · %d transfers (-%d)", h.EventTransfers, h.EventTransfersCost)
	}
	if team.Chip != "" {
		line += " · chip: " + team.Chip
	}
	fmt.Fprintf(w, "%s\n\n", line)

	printSquadTable(w, team.Squad, teams)

	// An existing squad can be worth more than the £100.0m starting budget once
	// prices rise, so every rule is checked except the budget.
	fmt.Fprintln(w)
	if err := optimizer.ValidateWithBudget(team.Squad, optimizer.NoBudgetLimit); err != nil {
		fmt.Fprintln(w, "Squad check: FAILED")
		var ve *optimizer.ValidationError
		if errors.As(err, &ve) {
			for _, v := range ve.Violations {
				fmt.Fprintf(w, "  - %s: %s\n", v.Rule, v.Detail)
			}
		}
	} else {
		fmt.Fprintln(w, "Squad check: OK")
	}

	short := make(map[int]string, len(teams))
	for _, t := range teams {
		short[t.ID] = t.ShortName
	}
	fmt.Fprintln(w, "\nAvailability")
	flagged := 0
	for _, p := range team.Squad.Players {
		chance := p.ChanceOfPlayingNextRound
		if p.Status == "a" && p.News == "" && (chance == nil || *chance >= 100) {
			continue
		}
		flagged++
		label := statusLabel(p.Status)
		if chance != nil && *chance < 100 {
			label += fmt.Sprintf(", %d%%", *chance)
		}
		fmt.Fprintf(w, "  ! %-14s %-4s %-18s %s\n", p.WebName, short[p.TeamID], label, p.News)
	}
	if flagged == 0 {
		fmt.Fprintln(w, "  No injury or availability flags")
	}
}

func statusLabel(s string) string {
	switch s {
	case "a":
		return "available"
	case "d":
		return "doubtful"
	case "i":
		return "injured"
	case "s":
		return "suspended"
	case "u":
		return "unavailable"
	}
	return s
}

// commas formats n with thousands separators: 1757258 -> "1,757,258".
func commas(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}
