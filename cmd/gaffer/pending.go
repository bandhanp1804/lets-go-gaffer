package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

// FPL publishes a squad only as of the last deadline; transfers made since are
// private to the logged-in manager. -swap lets the manager state them so the
// tools work from the squad they actually have.
const swapHelp = `  -swap      a transfer you have already made this week, as Out>In (repeatable), for
             example -swap "Havertz>Calvert-Lewin". FPL does not publish pending
             transfers without a login, so the tool takes your word for them.`

// stringList is a flag that may be given several times.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseTeamArgs reads the arguments shared by `team` and `lineup`: a team id (as
// -team, or one positional argument, defaulting to GAFFER_TEAM_ID) and any -swap
// flags. Flags must come before the id.
func parseTeamArgs(name, usage string, args []string) (int, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	teamFlag := fs.String("team", "", "")
	var swaps stringList
	fs.Var(&swaps, "swap", "")
	if err := fs.Parse(args); err != nil {
		return 0, nil, fmt.Errorf("%w\n%s", err, usage)
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && *teamFlag != "") {
		return 0, nil, errors.New(usage)
	}
	raw := os.Getenv("GAFFER_TEAM_ID")
	if *teamFlag != "" {
		raw = *teamFlag
	}
	if fs.NArg() == 1 {
		raw = fs.Arg(0)
	}
	if strings.TrimSpace(raw) == "" {
		return 0, nil, fmt.Errorf("a team id is needed: pass one, or set GAFFER_TEAM_ID\n%s", usage)
	}
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id < 1 {
		return 0, nil, fmt.Errorf("invalid team id %q\n%s", raw, usage)
	}
	return id, swaps, nil
}

// applyPending applies the manager's stated transfers to the squad and says so,
// because the tool is then no longer showing what FPL published. With no swaps it
// returns the team untouched and prints nothing.
func applyPending(w io.Writer, team myteam.Team, market []fpl.Player, swaps []string) (myteam.Team, []myteam.Change, error) {
	if len(swaps) == 0 {
		return team, nil, nil
	}
	specs, err := myteam.ParseSwaps(swaps)
	if err != nil {
		return team, nil, err
	}
	changes, err := myteam.ResolveSwaps(team.Squad.Players, market, specs)
	if err != nil {
		return team, nil, err
	}
	next, err := myteam.Apply(team, changes)
	if err != nil {
		return team, nil, err
	}
	fmt.Fprintf(w, "Including pending transfers you entered with -swap (not read from FPL): %s\n\n", myteam.DescribeChanges(changes))
	return next, changes, nil
}
