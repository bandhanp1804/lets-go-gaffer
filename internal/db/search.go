package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// SearchFilter narrows a news search. The zero value filters nothing, except that
// Limit must be positive.
type SearchFilter struct {
	PlayerIDs []int     // keep stories linked to any of these players; empty means all
	Since     time.Time // keep stories published at or after this; zero means any age
	Limit     int
}

// Hit is one search result with everything needed to cite it.
type Hit struct {
	Title       string
	Content     string
	URL         string
	Source      string
	PublishedAt time.Time
	Similarity  float64 // cosine similarity: 1 is identical, 0 unrelated
	PlayerIDs   []int
}

// SearchNews returns the stories closest to query by cosine similarity, best first.
// Filters apply before ranking, so a player or recency filter never leaves a
// result list shorter than Limit when enough matching stories exist.
func (s *Store) SearchNews(ctx context.Context, query []float32, f SearchFilter) ([]Hit, error) {
	if f.Limit <= 0 {
		return nil, nil
	}
	var since *time.Time
	if !f.Since.IsZero() {
		since = &f.Since
	}
	players := f.PlayerIDs
	if players == nil {
		players = []int{}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT a.title, c.content, a.url, a.source, a.published_at,
		       1 - (c.embedding <=> $1) AS similarity,
		       COALESCE((SELECT array_agg(cp.player_id ORDER BY cp.player_id)
		                 FROM chunk_players cp WHERE cp.chunk_id = c.id), '{}') AS player_ids
		FROM news_chunks c
		JOIN news_articles a ON a.id = c.article_id
		WHERE ($2::timestamptz IS NULL OR a.published_at >= $2)
		  AND (cardinality($3::int[]) = 0 OR EXISTS (
		        SELECT 1 FROM chunk_players cp WHERE cp.chunk_id = c.id AND cp.player_id = ANY($3)))
		  -- An FPL note is state: it only counts while it is the player's current
		  -- note (their news_added still equals its timestamp). Replaced or cleared
		  -- notes would otherwise report recovered players as injured.
		  AND (a.source <> $5 OR EXISTS (
		        SELECT 1 FROM chunk_players cp JOIN players p ON p.id = cp.player_id
		        WHERE cp.chunk_id = c.id AND p.news_added = a.published_at))
		ORDER BY c.embedding <=> $1, c.id
		LIMIT $4`,
		pgvector.NewVector(query), since, players, f.Limit, news.SourceFPL)
	if err != nil {
		return nil, fmt.Errorf("search news: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Title, &h.Content, &h.URL, &h.Source, &h.PublishedAt, &h.Similarity, &h.PlayerIDs); err != nil {
			return nil, fmt.Errorf("scan hit: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search news: %w", err)
	}
	return hits, nil
}

// FindPlayers returns players whose web name, last name, or full name equals name,
// ignoring case. It is an exact match on purpose: a loose match would silently
// filter a search to the wrong player.
func (s *Store) FindPlayers(ctx context.Context, name string) ([]fpl.Player, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, first_name, last_name, web_name, team_id, position,
			now_cost, total_points, form::text, minutes, status, chance_of_playing_next_round,
			news, news_added
		FROM players
		WHERE lower(web_name) = lower($1) OR lower(last_name) = lower($1)
		   OR lower(first_name || ' ' || last_name) = lower($1)
		ORDER BY id`, name)
	if err != nil {
		return nil, fmt.Errorf("find players %q: %w", name, err)
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
		return nil, fmt.Errorf("find players %q: %w", name, err)
	}
	return out, nil
}
