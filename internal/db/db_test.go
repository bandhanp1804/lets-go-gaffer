package db_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	_, err = pool.Exec(ctx, `TRUNCATE usage_counts, user_settings, alerts, watchlist, eval_runs, backtest_runs, briefings,
		chunk_players, news_chunks, news_articles, player_history, players, teams RESTART IDENTITY CASCADE`)
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
	files, _ := filepath.Glob("../../migrations/*.up.sql")
	if applied != len(files) || applied < 2 {
		t.Errorf("schema_migrations rows = %d, want %d (each migration applied exactly once)", applied, len(files))
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

func TestStore_PlayerNewsRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	added := time.Date(2026, 10, 21, 20, 12, 24, 123456000, time.UTC)
	injured, fit := player(1, 100), player(2, 61)
	injured.News, injured.NewsAdded = "Hamstring injury - 75% chance of playing", &added
	if err := s.UpsertPlayers(ctx, []fpl.Player{injured, fit}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPlayer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.News != injured.News || got.NewsAdded == nil || !got.NewsAdded.Equal(added) {
		t.Errorf("news = %q at %v, want %q at %v", got.News, got.NewsAdded, injured.News, added)
	}
	other, _ := s.GetPlayer(ctx, 2)
	if other.News != "" || other.NewsAdded != nil {
		t.Errorf("fit player news = %q at %v, want none", other.News, other.NewsAdded)
	}

	// The news is cleared when FPL clears it.
	injured.News, injured.NewsAdded = "", nil
	if err := s.UpsertPlayers(ctx, []fpl.Player{injured}); err != nil {
		t.Fatal(err)
	}
	if cleared, _ := s.GetPlayer(ctx, 1); cleared.News != "" || cleared.NewsAdded != nil {
		t.Errorf("after clearing: %q at %v, want none", cleared.News, cleared.NewsAdded)
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

func TestStore_ListPlayersAndTeams(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	// Empty database: empty results, not an error.
	if ps, err := s.ListPlayers(ctx); err != nil || len(ps) != 0 {
		t.Fatalf("empty ListPlayers = %v, %v; want none", ps, err)
	}

	pct := 50
	doubtful := player(2, 100)
	doubtful.ChanceOfPlayingNextRound = &pct
	teams := []fpl.Team{{ID: 2, Name: "Brighton", ShortName: "BHA"}, {ID: 1, Name: "Arsenal", ShortName: "ARS"}}
	if err := s.UpsertTeams(ctx, teams); err != nil {
		t.Fatal(err)
	}
	// Inserted out of id order on purpose.
	if err := s.UpsertPlayers(ctx, []fpl.Player{doubtful, player(1, 61)}); err != nil {
		t.Fatal(err)
	}

	ps, err := s.ListPlayers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != 1 || ps[1].ID != 2 {
		t.Fatalf("players = %+v, want ids [1 2] in ascending order", ps)
	}
	if ps[0].NowCost != 61 || ps[0].Form != "6.0" || ps[0].Position != fpl.MID || ps[0].ChanceOfPlayingNextRound != nil {
		t.Errorf("player 1 round trip lost data: %+v", ps[0])
	}
	if ps[1].ChanceOfPlayingNextRound == nil || *ps[1].ChanceOfPlayingNextRound != 50 {
		t.Errorf("player 2 chance = %v, want 50", ps[1].ChanceOfPlayingNextRound)
	}

	ts, err := s.ListTeams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].ShortName != "ARS" || ts[1].ShortName != "BHA" {
		t.Errorf("teams = %+v, want ARS then BHA (by id)", ts)
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

func TestStore_StartingPrices(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPlayers(ctx, []fpl.Player{player(1, 60), player(2, 60), player(3, 60)}); err != nil {
		t.Fatal(err)
	}
	err := s.UpsertHistory(ctx, []fpl.PlayerHistory{
		{PlayerID: 1, GameweekID: 1, Fixture: 1, Value: 60},
		{PlayerID: 1, GameweekID: 2, Fixture: 11, Value: 61}, // a later, higher price must not win
		{PlayerID: 2, GameweekID: 1, Fixture: 2, Value: 45},
		{PlayerID: 3, GameweekID: 4, Fixture: 3, Value: 80}, // no gameweek 1 row
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.StartingPrices(ctx, []int{1, 2, 3, 99})

	if err != nil {
		t.Fatal(err)
	}
	// The price in gameweek 1 is the launch price: what an original squad paid.
	if len(got) != 2 || got[1] != 60 || got[2] != 45 {
		t.Errorf("prices = %v, want map[1:60 2:45] (3 has no gameweek 1 row, 99 is unknown)", got)
	}
	if empty, err := s.StartingPrices(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no ids = %v, %v", empty, err)
	}
}

func TestStore_ListHistory(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPlayers(ctx, []fpl.Player{player(1, 60), player(2, 60)}); err != nil {
		t.Fatal(err)
	}
	rows := []fpl.PlayerHistory{
		{PlayerID: 2, GameweekID: 1, Fixture: 3, TotalPoints: 2, Minutes: 90, Value: 60},
		{PlayerID: 1, GameweekID: 2, Fixture: 11, TotalPoints: 9, Minutes: 85, GoalsScored: 1, Value: 61},
		{PlayerID: 1, GameweekID: 1, Fixture: 1, TotalPoints: 6, Minutes: 90, Value: 60},
		{PlayerID: 1, GameweekID: 2, Fixture: 12, TotalPoints: 2, Minutes: 20, Value: 61}, // a double gameweek
	}
	if err := s.UpsertHistory(ctx, rows); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListHistory(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("rows = %d, want 4", len(got))
	}
	// Ordered by player, then gameweek, then fixture, whatever the insert order.
	order := [][3]int{{1, 1, 1}, {1, 2, 11}, {1, 2, 12}, {2, 1, 3}}
	for i, want := range order {
		if got[i].PlayerID != want[0] || got[i].GameweekID != want[1] || got[i].Fixture != want[2] {
			t.Errorf("row %d = player %d gw %d fixture %d, want %v", i, got[i].PlayerID, got[i].GameweekID, got[i].Fixture, want)
		}
	}
	if got[1].TotalPoints != 9 || got[1].Minutes != 85 || got[1].GoalsScored != 1 || got[1].Value != 61 {
		t.Errorf("row lost data: %+v", got[1])
	}
}
