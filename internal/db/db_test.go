package db_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// Integration tests: they need a real Postgres with pgvector.
//
//	docker compose -f deploy/docker/docker-compose.yml up -d
//	docker compose -f deploy/docker/docker-compose.yml exec postgres createdb -U gaffer gaffer_test
//	GAFFER_TEST_DSN='postgres://gaffer:gaffer@localhost:5433/gaffer_test?sslmode=disable' go test ./internal/db/
//
// Without GAFFER_TEST_DSN they skip, so plain `go test ./...` stays green.

// newStore connects, migrates, and empties every table. It refuses to run
// against any database whose name does not end in _test, because it truncates.
func newStore(t *testing.T) (*db.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("GAFFER_TEST_DSN")
	if dsn == "" {
		t.Skip("GAFFER_TEST_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to truncate database %q: name must end in _test", cfg.ConnConfig.Database)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, os.DirFS("../../migrations")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, err = pool.Exec(ctx, `TRUNCATE chunk_players, news_chunks, news_articles,
		player_history, players, teams RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db.NewStore(pool), pool
}

func arsenal() []fpl.Team { return []fpl.Team{{ID: 1, Name: "Arsenal", ShortName: "ARS"}} }

func player(id int, cost int) fpl.Player {
	return fpl.Player{
		ID: id, FirstName: "First", LastName: "Last", WebName: "Web",
		TeamID: 1, Position: fpl.MID, NowCost: cost, TotalPoints: 10,
		Form: "6.0", Minutes: 900, Status: "a",
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	_, pool := newStore(t)
	ctx := context.Background()

	// newStore already migrated once; a second run must be a no-op.
	if err := db.Migrate(ctx, pool, os.DirFS("../../migrations")); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("schema_migrations rows = %d, want 1 (one migration, applied once)", applied)
	}

	for _, table := range []string{"teams", "players", "player_history", "news_articles", "news_chunks", "chunk_players"} {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err=%v)", table, err)
		}
	}
}

func TestOpen_EveryPooledConnectionKnowsTheVectorType(t *testing.T) {
	_, pool := newStore(t)
	ctx := context.Background()

	// Hold several connections at once so the pool must open more than one;
	// type registration has to happen per connection, not once.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer conn.Release()
			time.Sleep(50 * time.Millisecond) // force overlap

			var v pgvector.Vector
			err = conn.QueryRow(ctx, `SELECT '[1,2,3]'::vector`).Scan(&v)
			if err != nil {
				t.Errorf("scan vector: %v", err)
				return
			}
			if got := v.Slice(); len(got) != 3 || got[2] != 3 {
				t.Errorf("vector = %v, want [1 2 3]", got)
			}
		}()
	}
	wg.Wait()
}

func TestStore_UpsertPlayers(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertPlayers(ctx, []fpl.Player{player(1, 61), player(2, 100)}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountPlayers(ctx); err != nil || n != 2 {
		t.Fatalf("count = %d (err %v), want 2", n, err)
	}

	// Same ids, new price: must update in place rather than duplicate.
	if err := s.UpsertPlayers(ctx, []fpl.Player{player(1, 62), player(2, 100)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPlayers(ctx); n != 2 {
		t.Errorf("count after re-upsert = %d, want 2", n)
	}
	got, err := s.GetPlayer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.NowCost != 62 {
		t.Errorf("NowCost = %d, want 62 (updated)", got.NowCost)
	}
	if got.Form != "6.0" || got.Position != fpl.MID || got.TeamID != 1 {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestStore_ChanceOfPlayingRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}

	pct := 75
	fit, doubtful := player(1, 61), player(2, 100)
	doubtful.ChanceOfPlayingNextRound = &pct
	if err := s.UpsertPlayers(ctx, []fpl.Player{fit, doubtful}); err != nil {
		t.Fatal(err)
	}

	gotFit, _ := s.GetPlayer(ctx, 1)
	gotDoubtful, _ := s.GetPlayer(ctx, 2)
	// NULL (not assessed) must stay distinct from a real percentage.
	if gotFit.ChanceOfPlayingNextRound != nil {
		t.Errorf("fit player chance = %d, want nil", *gotFit.ChanceOfPlayingNextRound)
	}
	if gotDoubtful.ChanceOfPlayingNextRound == nil || *gotDoubtful.ChanceOfPlayingNextRound != 75 {
		t.Errorf("doubtful chance = %v, want 75", gotDoubtful.ChanceOfPlayingNextRound)
	}
}

func TestStore_UpsertPlayersRequiresTeam(t *testing.T) {
	s, _ := newStore(t) // note: no teams inserted
	ctx := context.Background()

	err := s.UpsertPlayers(ctx, []fpl.Player{player(1, 61)})

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" { // foreign_key_violation
		t.Fatalf("err = %v, want a wrapped foreign key violation (23503)", err)
	}
	if n, _ := s.CountPlayers(ctx); n != 0 {
		t.Errorf("count = %d, want 0 (failed upsert must not leave rows)", n)
	}
}

func TestStore_UpsertHistory(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPlayers(ctx, []fpl.Player{player(7, 65)}); err != nil {
		t.Fatal(err)
	}

	rows := []fpl.PlayerHistory{
		{PlayerID: 7, GameweekID: 3, Fixture: 21, TotalPoints: 9, Minutes: 90, Value: 65},
		// Double gameweek: same player and gameweek, different fixture.
		{PlayerID: 7, GameweekID: 3, Fixture: 25, TotalPoints: 2, Minutes: 60, Value: 65},
	}
	if err := s.UpsertHistory(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountHistory(ctx, 7); err != nil || n != 2 {
		t.Fatalf("count = %d (err %v), want 2 (both double-gameweek fixtures)", n, err)
	}

	// Re-ingesting the same rows with a corrected score must not duplicate.
	rows[0].TotalPoints = 11
	if err := s.UpsertHistory(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountHistory(ctx, 7); n != 2 {
		t.Errorf("count after re-upsert = %d, want 2", n)
	}
	got, err := s.TotalHistoryPoints(ctx, 7)
	if err != nil || got != 13 { // 11 + 2
		t.Errorf("points = %d (err %v), want 13", got, err)
	}
}

func TestStore_EmptyUpsertsAreNoOps(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	if err := s.UpsertTeams(ctx, nil); err != nil {
		t.Errorf("UpsertTeams(nil): %v", err)
	}
	if err := s.UpsertPlayers(ctx, nil); err != nil {
		t.Errorf("UpsertPlayers(nil): %v", err)
	}
	if err := s.UpsertHistory(ctx, []fpl.PlayerHistory{}); err != nil {
		t.Errorf("UpsertHistory(empty): %v", err)
	}
}

func TestStore_CancelledContextFails(t *testing.T) {
	s, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.UpsertTeams(ctx, arsenal())

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
