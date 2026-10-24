package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// FPL publishes a squad only as of the last deadline; transfers made since are
// visible only to the logged-in manager. With FPL_AUTH_HEADER set to the
// x-api-authorization value from a logged-in browser, the tool reads the live
// squad, with FPL's own prices and transfer counts. The token lasts about an
// hour, so any problem with it falls back to public data and says why.
const sessionEnv = "FPL_AUTH_HEADER"

// soonToExpire is when a still-valid token earns a warning.
const soonToExpire = 10 * time.Minute

// liveSource is the part of fpl.HTTPClient that reading the live squad needs.
type liveSource interface {
	FetchMyTeam(ctx context.Context, teamID int) (*fpl.MyTeam, error)
}

// sessionClient is an HTTP client that logs requests to FPL in as the manager.
// The token is attached only for FPL's own host and is never logged.
func sessionClient(token string) *http.Client {
	return &http.Client{Transport: httpx.NewRetryTransport(
		httpx.NewRateLimitTransport(fpl.NewSessionTransport(http.DefaultTransport, token, fpl.Host), httpx.NewLimiter(100*time.Millisecond)),
		httpx.RetryConfig{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second},
	)}
}

// tryLive reads the manager's live squad, or returns nil and says why not. It never
// fails the command: a missing, expired, or rejected session just means public data.
// Nothing it prints contains the token.
func tryLive(ctx context.Context, w io.Writer, src liveSource, session string, teamID int, now time.Time) *fpl.MyTeam {
	if strings.TrimSpace(session) == "" {
		return nil
	}
	expiry, known := fpl.TokenExpiry(session)
	if known && !expiry.After(now) {
		fmt.Fprintf(w, "Your FPL session expired %s ago (they last about an hour), so only public data is used. "+
			"Copy a fresh x-api-authorization value into %s in .env.\n\n", ago(now.Sub(expiry)), sessionEnv)
		return nil
	}

	live, err := src.FetchMyTeam(ctx, teamID)
	switch {
	case errors.Is(err, fpl.ErrSessionRejected):
		fmt.Fprintf(w, "FPL rejected your session (it has probably expired); using public data. "+
			"Copy a fresh x-api-authorization value into %s in .env.\n\n", sessionEnv)
		return nil
	case err != nil:
		fmt.Fprintf(w, "Could not read your live squad from FPL (%v); using public data.\n\n", err)
		return nil
	}

	msg := "Read your live squad from FPL (includes transfers made since the last deadline)."
	if known {
		left := expiry.Sub(now)
		msg += fmt.Sprintf(" Session valid for another %d min", int(left.Minutes()))
		if left < soonToExpire {
			msg += ": refresh " + sessionEnv + " soon"
		}
		msg += "."
	}
	fmt.Fprintf(w, "%s\n\n", msg)
	return live
}

// ago renders an elapsed time coarsely: "3h" or "42 min".
func ago(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// currentSquad builds the squad to analyse: the live one from FPL if a session
// works, otherwise the public one with any -swap applied. fin is FPL's own
// finances when live, or nil when the caller must work them out. Swaps are
// ignored for a live squad, which already includes the manager's transfers.
func currentSquad(ctx context.Context, w io.Writer, src liveSource, session string, teamID int,
	public myteam.Team, players []fpl.Player, swaps []string, now time.Time) (myteam.Team, *myteam.Finances, []myteam.Change, error) {

	if live := tryLive(ctx, w, src, session, teamID, now); live != nil {
		team, fin, err := myteam.WithLiveSquad(public, live, players)
		if err != nil {
			return myteam.Team{}, nil, nil, err
		}
		if len(swaps) > 0 {
			fmt.Fprint(w, "-swap is ignored: your live squad already includes the transfers you have made.\n\n")
		}
		return team, &fin, nil, nil
	}
	team, changes, err := applyPending(w, public, players, swaps)
	return team, nil, changes, err
}

// liveSummary is a one-line statement of the manager's live position.
func liveSummary(fin myteam.Finances) string {
	parts := []string{fmt.Sprintf("Live from FPL: bank %s", formatPrice(fin.Bank))}
	switch {
	case fin.PendingTransfers == 0:
		parts = append(parts, "no transfers made this week")
	case fin.PendingTransfers == 1:
		parts = append(parts, "1 transfer made this week")
	default:
		parts = append(parts, fmt.Sprintf("%d transfers made this week", fin.PendingTransfers))
	}
	if fin.PendingHit > 0 {
		parts[len(parts)-1] += fmt.Sprintf(", -%d points so far", fin.PendingHit)
	}
	parts = append(parts, freeTransfers(fin.Free.Next)+" left")
	if len(fin.AvailableChips) > 0 {
		parts = append(parts, "chips available: "+strings.Join(fin.AvailableChips, ", "))
	}
	return strings.Join(parts, " · ")
}

// scheduleSource is the part of fpl.HTTPClient that planGameweek needs.
type scheduleSource interface {
	FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error)
}

// planGameweek returns the gameweek being decided for, taken from FPL's schedule, and
// where the stored history ends. They differ while a gameweek is under way: its
// deadline has passed, so it is locked, but its history is not in yet. It prints a note
// when the stored history is behind a finished gameweek.
func planGameweek(ctx context.Context, w io.Writer, src scheduleSource, history []fpl.PlayerHistory) (upcoming, asOf int, err error) {
	boot, err := src.FetchBootstrap(ctx)
	if err != nil {
		return 0, 0, err
	}
	upcoming = fpl.UpcomingGameweek(boot.Gameweeks)
	if upcoming == 0 {
		return 0, 0, errors.New("there is no upcoming gameweek (the season may be over)")
	}
	asOf = projection.AsOf(history)
	if note := projection.HistoryNote(asOf, fpl.LastFinishedGameweek(boot.Gameweeks)); note != "" {
		fmt.Fprintf(w, "%s\n\n", note)
	}
	return upcoming, asOf, nil
}
