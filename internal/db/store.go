package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// Store reads and writes FPL data.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// execBatch runs sql once per index 0..n-1 inside a single transaction, so a bulk
// upsert either fully applies or leaves nothing behind. n == 0 is a no-op.
func (s *Store) execBatch(ctx context.Context, sql string, n int, args func(i int) []any) error {
	if n == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	batch := &pgx.Batch{}
	for i := 0; i < n; i++ {
		batch.Queue(sql, args(i)...)
	}
	results := tx.SendBatch(ctx, batch)
	for i := 0; i < n; i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("row %d: %w", i, err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *Store) UpsertTeams(ctx context.Context, teams []fpl.Team) error {
	const q = `INSERT INTO teams (id, name, short_name) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, short_name = EXCLUDED.short_name`
	err := s.execBatch(ctx, q, len(teams), func(i int) []any {
		t := teams[i]
		return []any{t.ID, t.Name, t.ShortName}
	})
	if err != nil {
		return fmt.Errorf("upsert teams: %w", err)
	}
	return nil
}

func (s *Store) UpsertPlayers(ctx context.Context, players []fpl.Player) error {
	// form arrives as text ("6.0"); the cast keeps pgx from guessing an encoding.
	const q = `INSERT INTO players (id, first_name, last_name, web_name, team_id, position,
			now_cost, total_points, form, minutes, status, chance_of_playing_next_round,
			news, news_added)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE(NULLIF($9::text, ''), '0')::numeric, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name,
			web_name = EXCLUDED.web_name, team_id = EXCLUDED.team_id,
			position = EXCLUDED.position, now_cost = EXCLUDED.now_cost,
			total_points = EXCLUDED.total_points, form = EXCLUDED.form,
			minutes = EXCLUDED.minutes, status = EXCLUDED.status,
			chance_of_playing_next_round = EXCLUDED.chance_of_playing_next_round,
			news = EXCLUDED.news, news_added = EXCLUDED.news_added,
			updated_at = now()`
	err := s.execBatch(ctx, q, len(players), func(i int) []any {
		p := players[i]
		return []any{p.ID, p.FirstName, p.LastName, p.WebName, p.TeamID, int(p.Position),
			p.NowCost, p.TotalPoints, p.Form, p.Minutes, p.Status, p.ChanceOfPlayingNextRound,
			p.News, p.NewsAdded}
	})
	if err != nil {
		return fmt.Errorf("upsert players: %w", err)
	}
	return nil
}

func (s *Store) UpsertHistory(ctx context.Context, rows []fpl.PlayerHistory) error {
	const q = `INSERT INTO player_history (player_id, gameweek, fixture, total_points, minutes,
			goals_scored, assists, clean_sheets, yellow_cards, red_cards, value)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (player_id, gameweek, fixture) DO UPDATE SET
			total_points = EXCLUDED.total_points, minutes = EXCLUDED.minutes,
			goals_scored = EXCLUDED.goals_scored, assists = EXCLUDED.assists,
			clean_sheets = EXCLUDED.clean_sheets, yellow_cards = EXCLUDED.yellow_cards,
			red_cards = EXCLUDED.red_cards, value = EXCLUDED.value`
	err := s.execBatch(ctx, q, len(rows), func(i int) []any {
		h := rows[i]
		return []any{h.PlayerID, h.GameweekID, h.Fixture, h.TotalPoints, h.Minutes,
			h.GoalsScored, h.Assists, h.CleanSheets, h.YellowCards, h.RedCards, h.Value}
	})
	if err != nil {
		return fmt.Errorf("upsert history: %w", err)
	}
	return nil
}

func (s *Store) CountPlayers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM players`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count players: %w", err)
	}
	return n, nil
}

func (s *Store) GetPlayer(ctx context.Context, id int) (fpl.Player, error) {
	var p fpl.Player
	var position int
	err := s.pool.QueryRow(ctx, `SELECT id, first_name, last_name, web_name, team_id, position,
			now_cost, total_points, form::text, minutes, status, chance_of_playing_next_round,
			news, news_added
		FROM players WHERE id = $1`, id).Scan(
		&p.ID, &p.FirstName, &p.LastName, &p.WebName, &p.TeamID, &position,
		&p.NowCost, &p.TotalPoints, &p.Form, &p.Minutes, &p.Status, &p.ChanceOfPlayingNextRound,
		&p.News, &p.NewsAdded)
	if errors.Is(err, pgx.ErrNoRows) {
		return fpl.Player{}, fmt.Errorf("player %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fpl.Player{}, fmt.Errorf("get player %d: %w", id, err)
	}
	p.Position = fpl.Position(position)
	return p, nil
}

// ListPlayers returns every player, ascending by id.
func (s *Store) ListPlayers(ctx context.Context) ([]fpl.Player, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, first_name, last_name, web_name, team_id, position,
			now_cost, total_points, form::text, minutes, status, chance_of_playing_next_round,
			news, news_added
		FROM players ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}
	defer rows.Close()

	var out []fpl.Player
	for rows.Next() {
		var p fpl.Player
		var position int
		err := rows.Scan(&p.ID, &p.FirstName, &p.LastName, &p.WebName, &p.TeamID, &position,
			&p.NowCost, &p.TotalPoints, &p.Form, &p.Minutes, &p.Status, &p.ChanceOfPlayingNextRound,
			&p.News, &p.NewsAdded)
		if err != nil {
			return nil, fmt.Errorf("scan player: %w", err)
		}
		p.Position = fpl.Position(position)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}
	return out, nil
}

// ListTeams returns every team, ascending by id.
func (s *Store) ListTeams(ctx context.Context) ([]fpl.Team, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, short_name FROM teams ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var out []fpl.Team
	for rows.Next() {
		var t fpl.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.ShortName); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return out, nil
}

func (s *Store) CountHistory(ctx context.Context, playerID int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM player_history WHERE player_id = $1`, playerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count history for %d: %w", playerID, err)
	}
	return n, nil
}

func (s *Store) TotalHistoryPoints(ctx context.Context, playerID int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(total_points), 0)::int FROM player_history WHERE player_id = $1`, playerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sum history for %d: %w", playerID, err)
	}
	return n, nil
}

// StartingPrices returns each player's price in gameweek 1, which is the price
// everyone in an original squad paid. Players with no gameweek 1 row are left
// out rather than guessed.
func (s *Store) StartingPrices(ctx context.Context, playerIDs []int) (map[int]int, error) {
	out := map[int]int{}
	if len(playerIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT player_id, min(value) FROM player_history WHERE gameweek = 1 AND player_id = ANY($1) GROUP BY player_id`,
		playerIDs)
	if err != nil {
		return nil, fmt.Errorf("starting prices: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, price int
		if err := rows.Scan(&id, &price); err != nil {
			return nil, fmt.Errorf("scan starting price: %w", err)
		}
		out[id] = price
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("starting prices: %w", err)
	}
	return out, nil
}

// ListHistory returns every stored player-gameweek row, ordered by player, then
// gameweek, then fixture (a double gameweek has two rows for one player).
func (s *Store) ListHistory(ctx context.Context) ([]fpl.PlayerHistory, error) {
	rows, err := s.pool.Query(ctx, `SELECT player_id, gameweek, fixture, total_points, minutes,
			goals_scored, assists, clean_sheets, yellow_cards, red_cards, value
		FROM player_history ORDER BY player_id, gameweek, fixture`)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	var out []fpl.PlayerHistory
	for rows.Next() {
		var h fpl.PlayerHistory
		err := rows.Scan(&h.PlayerID, &h.GameweekID, &h.Fixture, &h.TotalPoints, &h.Minutes,
			&h.GoalsScored, &h.Assists, &h.CleanSheets, &h.YellowCards, &h.RedCards, &h.Value)
		if err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	return out, nil
}
