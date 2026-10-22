package ingest

import (
	"context"
	"fmt"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// Client is satisfied by fpl.FPLClient.
type Client interface {
	FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error)
	HistoryFetcher
}

// Store is satisfied by db.Store.
type Store interface {
	UpsertTeams(ctx context.Context, teams []fpl.Team) error
	UpsertPlayers(ctx context.Context, players []fpl.Player) error
	HistoryStore
}

// BootstrapClient is the part of fpl.FPLClient that Bootstrap needs.
type BootstrapClient interface {
	FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error)
}

// BootstrapStore is the part of db.Store that Bootstrap needs.
type BootstrapStore interface {
	UpsertTeams(ctx context.Context, teams []fpl.Team) error
	UpsertPlayers(ctx context.Context, players []fpl.Player) error
}

// Bootstrap fetches the season snapshot (one API call) and stores teams, then
// players, in that order because players reference teams. It is the cheap part
// of Sync, enough to refresh prices, availability, and FPL's player news. The
// snapshot is returned so callers need not fetch it again.
func Bootstrap(ctx context.Context, c BootstrapClient, s BootstrapStore) (*fpl.Bootstrap, error) {
	b, err := c.FetchBootstrap(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if err := s.UpsertTeams(ctx, b.Teams); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if err := s.UpsertPlayers(ctx, b.Players); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return b, nil
}

// Sync runs a full ingest: Bootstrap, then every player's history through the
// worker pool.
//
// Order matters because of foreign keys: players reference teams and history
// references players. If any step before the history pool fails, nothing after
// it runs. The pool's Summary is returned even when it aborts partway.
func Sync(ctx context.Context, c Client, s Store, cfg Config) (Summary, error) {
	b, err := Bootstrap(ctx, c, s)
	if err != nil {
		return Summary{}, fmt.Errorf("sync: %w", err)
	}

	ids := make([]int, len(b.Players))
	for i, p := range b.Players {
		ids[i] = p.ID
	}
	sum, err := Histories(ctx, c, s, ids, cfg)
	if err != nil {
		return sum, fmt.Errorf("sync: %w", err)
	}
	return sum, nil
}
