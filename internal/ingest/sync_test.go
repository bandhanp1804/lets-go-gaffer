package ingest_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl/fpltest"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
)

// memStore records the order of writes, since foreign keys make order matter.
type memStore struct {
	failPlayers error

	mu      sync.Mutex
	order   []string
	teams   []fpl.Team
	players []fpl.Player
	history map[int][]fpl.PlayerHistory
}

func (m *memStore) UpsertTeams(_ context.Context, t []fpl.Team) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.order = append(m.order, "teams")
	m.teams = t
	return nil
}

func (m *memStore) UpsertPlayers(_ context.Context, p []fpl.Player) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.order = append(m.order, "players")
	if m.failPlayers != nil {
		return m.failPlayers
	}
	m.players = p
	return nil
}

func (m *memStore) UpsertHistory(_ context.Context, rows []fpl.PlayerHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	if m.history == nil {
		m.history = map[int][]fpl.PlayerHistory{}
	}
	m.order = append(m.order, "history")
	m.history[rows[0].PlayerID] = rows
	return nil
}

func syncFixture(n int) (*fpltest.Fake, *fpl.Bootstrap) {
	b := &fpl.Bootstrap{Teams: []fpl.Team{{ID: 1, Name: "Arsenal", ShortName: "ARS"}}}
	hist := map[int][]fpl.PlayerHistory{}
	for id := 1; id <= n; id++ {
		b.Players = append(b.Players, fpl.Player{ID: id, TeamID: 1})
		hist[id] = []fpl.PlayerHistory{{PlayerID: id, GameweekID: 1, Fixture: id}}
	}
	return fpltest.NewFake(b, hist), b
}

func TestSync_StoresTeamsThenPlayersThenHistories(t *testing.T) {
	client, b := syncFixture(12)
	store := &memStore{}

	sum, err := ingest.Sync(context.Background(), client, store, ingest.Config{Workers: 4})

	if err != nil {
		t.Fatal(err)
	}
	// Players reference teams, so teams must land first; histories reference players.
	if got := store.order[:2]; !slices.Equal(got, []string{"teams", "players"}) {
		t.Errorf("first writes = %v, want [teams players]", got)
	}
	if slices.Contains(store.order[:2], "history") {
		t.Error("history written before teams and players")
	}
	if len(store.teams) != 1 || len(store.players) != len(b.Players) {
		t.Errorf("stored %d teams, %d players; want 1 and %d", len(store.teams), len(store.players), len(b.Players))
	}
	if sum.Done != 12 || sum.Rows != 12 || len(store.history) != 12 {
		t.Errorf("summary = %+v, histories stored = %d; want 12 each", sum, len(store.history))
	}
	if client.BootstrapCalls() != 1 {
		t.Errorf("bootstrap fetched %d times, want 1", client.BootstrapCalls())
	}
}

func TestSync_BootstrapFailureStoresNothing(t *testing.T) {
	client, _ := syncFixture(3)
	client.SetErr(errFetch)
	store := &memStore{}

	_, err := ingest.Sync(context.Background(), client, store, ingest.Config{Workers: 2})

	if !errors.Is(err, errFetch) {
		t.Fatalf("err = %v, want it to wrap %v", err, errFetch)
	}
	if len(store.order) != 0 {
		t.Errorf("writes = %v, want none", store.order)
	}
}

func TestSync_PlayerStoreFailureSkipsHistories(t *testing.T) {
	client, _ := syncFixture(3)
	store := &memStore{failPlayers: errStore}

	_, err := ingest.Sync(context.Background(), client, store, ingest.Config{Workers: 2})

	if !errors.Is(err, errStore) {
		t.Fatalf("err = %v, want it to wrap %v", err, errStore)
	}
	// With no players stored, history rows would violate the foreign key.
	for id := 1; id <= 3; id++ {
		if n := client.HistoryCalls(id); n != 0 {
			t.Errorf("player %d history fetched %d times, want 0", id, n)
		}
	}
}

func TestSync_PropagatesHistoryAbort(t *testing.T) {
	// A fake with players but no history data: every history fetch fails.
	_, b := syncFixture(20)
	client := fpltest.NewFake(b, map[int][]fpl.PlayerHistory{})

	sum, err := ingest.Sync(context.Background(), client, &memStore{}, ingest.Config{Workers: 2, MaxFailures: 1})

	if !errors.Is(err, ingest.ErrTooManyFailures) {
		t.Fatalf("err = %v, want ErrTooManyFailures", err)
	}
	if len(sum.Failed) < 2 {
		t.Errorf("failed = %d, want the partial summary to be returned", len(sum.Failed))
	}
}

func TestBootstrap_StoresTeamsThenPlayersAndReturnsThem(t *testing.T) {
	client, b := syncFixture(5)
	store := &memStore{}

	got, err := ingest.Bootstrap(context.Background(), client, store)

	if err != nil {
		t.Fatal(err)
	}
	if got != b {
		t.Error("want the fetched bootstrap returned so callers can reuse it without a second API call")
	}
	if !slices.Equal(store.order, []string{"teams", "players"}) {
		t.Errorf("writes = %v, want [teams players] and no history", store.order)
	}
	if client.BootstrapCalls() != 1 {
		t.Errorf("bootstrap fetched %d times, want 1", client.BootstrapCalls())
	}
	for id := 1; id <= 5; id++ {
		if client.HistoryCalls(id) != 0 {
			t.Errorf("history fetched for player %d; Bootstrap must stay cheap", id)
		}
	}
}

func TestBootstrap_Failures(t *testing.T) {
	t.Run("fetch fails", func(t *testing.T) {
		client, _ := syncFixture(2)
		client.SetErr(errFetch)
		store := &memStore{}

		got, err := ingest.Bootstrap(context.Background(), client, store)

		if !errors.Is(err, errFetch) || got != nil || len(store.order) != 0 {
			t.Errorf("got=%v err=%v writes=%v; want nil, wrapped errFetch, no writes", got, err, store.order)
		}
	})
	t.Run("player store fails", func(t *testing.T) {
		client, _ := syncFixture(2)

		got, err := ingest.Bootstrap(context.Background(), client, &memStore{failPlayers: errStore})

		if !errors.Is(err, errStore) || got != nil {
			t.Errorf("got=%v err=%v; want nil and wrapped errStore", got, err)
		}
	})
}
