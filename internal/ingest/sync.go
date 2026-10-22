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

// Sync runs a full ingest: bootstrap (teams, then players), then every player's
// history through the worker pool.
//
// Order matters because of foreign keys: players reference teams and history
// references players. If any step before the history pool fails, nothing after
// it runs. The pool's Summary is returned even when it aborts partway.
func Sync(ctx context.Context, c Client, s Store, cfg Config) (Summary, error) {
	b, err := c.FetchBootstrap(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("sync: %w", err)
	}
	if err := s.UpsertTeams(ctx, b.Teams); err != nil {
		return Summary{}, fmt.Errorf("sync: %w", err)
	}
	if err := s.UpsertPlayers(ctx, b.Players); err != nil {
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
