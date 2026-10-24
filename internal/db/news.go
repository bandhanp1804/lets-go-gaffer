package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// NewsCounts is how many rows each news table holds.
type NewsCounts struct {
	Articles    int
	Chunks      int
	PlayerLinks int
	Newest      time.Time // when the newest stored story was published; zero with none
}

// ExistingURLs returns which of urls are already stored as articles.
func (s *Store) ExistingURLs(ctx context.Context, urls []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(urls) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT url FROM news_articles WHERE url = ANY($1)`, urls)
	if err != nil {
		return nil, fmt.Errorf("look up stored urls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan url: %w", err)
		}
		out[u] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("look up stored urls: %w", err)
	}
	return out, nil
}

// SaveNews stores each item as an article with one chunk (its title and summary
// text), the chunk's embedding, and links to the players it mentions. All items
// are saved in one transaction: if any fails, none are. An item whose URL is
// already stored is skipped, so two overlapping runs cannot duplicate it.
//
// News items here are headline-sized, so each has a single chunk. The schema
// keeps chunk_idx for when longer article text is added.
func (s *Store) SaveNews(ctx context.Context, items []news.Embedded) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	for _, it := range items {
		var articleID int64
		err := tx.QueryRow(ctx, `INSERT INTO news_articles (url, source, title, published_at)
			VALUES ($1, $2, $3, $4) ON CONFLICT (url) DO NOTHING RETURNING id`,
			it.URL, it.Source, it.Title, it.PublishedAt).Scan(&articleID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // already stored
		}
		if err != nil {
			return fmt.Errorf("save article %s: %w", it.URL, err)
		}

		var chunkID int64
		err = tx.QueryRow(ctx, `INSERT INTO news_chunks (article_id, chunk_idx, content, embedding)
			VALUES ($1, 0, $2, $3) RETURNING id`,
			articleID, it.Text(), pgvector.NewVector(it.Embedding)).Scan(&chunkID)
		if err != nil {
			return fmt.Errorf("save chunk for %s: %w", it.URL, err)
		}

		playerIDs := slices.Clone(it.PlayerIDs)
		slices.Sort(playerIDs)
		for _, pid := range slices.Compact(playerIDs) {
			if _, err := tx.Exec(ctx,
				`INSERT INTO chunk_players (chunk_id, player_id) VALUES ($1, $2)`, chunkID, pid); err != nil {
				return fmt.Errorf("link %s to player %d: %w", it.URL, pid, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// CountNews returns the size of each news table.
func (s *Store) CountNews(ctx context.Context) (NewsCounts, error) {
	var c NewsCounts
	var newest *time.Time // null when there are no stories
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM news_articles),
		(SELECT count(*) FROM news_chunks),
		(SELECT count(*) FROM chunk_players),
		(SELECT max(published_at) FROM news_articles)`).Scan(&c.Articles, &c.Chunks, &c.PlayerLinks, &newest)
	if err != nil {
		return NewsCounts{}, fmt.Errorf("count news: %w", err)
	}
	if newest != nil {
		c.Newest = *newest
	}
	return c, nil
}

// RelinkNews works out again which players each stored story mentions, using link on the story's
// stored text (its title and summary), and updates the links that changed. It is for after the linker improves: stories
// already stored were linked by the old rules. Stories from FPL's own notes are left alone, since
// each is about exactly the player it was written for. It returns how many stories' links changed.
func (s *Store) RelinkNews(ctx context.Context, link func(text string) []int) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.content,
			COALESCE((SELECT array_agg(cp.player_id ORDER BY cp.player_id) FROM chunk_players cp WHERE cp.chunk_id = c.id), '{}')
		FROM news_chunks c JOIN news_articles a ON a.id = c.article_id
		WHERE a.source <> $1`, news.SourceFPL)
	if err != nil {
		return 0, fmt.Errorf("read stories to relink: %w", err)
	}
	type change struct {
		chunk int64
		ids   []int
	}
	var changes []change
	for rows.Next() {
		var chunk int64
		var content string
		var old []int32
		if err := rows.Scan(&chunk, &content, &old); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan story: %w", err)
		}
		want := link(content) // the chunk holds the title and the summary
		if !slices.EqualFunc(want, old, func(a int, b int32) bool { return a == int(b) }) {
			changes = append(changes, change{chunk, want})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read stories to relink: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	for _, c := range changes {
		if _, err := tx.Exec(ctx, `DELETE FROM chunk_players WHERE chunk_id = $1`, c.chunk); err != nil {
			return 0, fmt.Errorf("clear links of chunk %d: %w", c.chunk, err)
		}
		for _, pid := range c.ids {
			if _, err := tx.Exec(ctx, `INSERT INTO chunk_players (chunk_id, player_id) VALUES ($1, $2)`, c.chunk, pid); err != nil {
				return 0, fmt.Errorf("link chunk %d to player %d: %w", c.chunk, pid, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(changes), nil
}
