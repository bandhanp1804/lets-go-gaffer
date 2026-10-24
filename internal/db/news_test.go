package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// vec1024 returns a 1024-dim vector whose first element is x (the column width).
func vec1024(x float32) []float32 {
	v := make([]float32, 1024)
	v[0] = x
	return v
}

func story(url, title string, x float32, players ...int) news.Embedded {
	return news.Embedded{
		Item: news.Item{
			Source: "bbc", Title: title, Summary: "Some detail.", URL: url,
			PublishedAt: time.Date(2026, 10, 21, 22, 51, 56, 0, time.UTC), PlayerIDs: players,
		},
		Embedding: vec1024(x),
	}
}

// withPlayers creates a team and the given players so links have rows to point at.
func withPlayers(t *testing.T, s *db.Store, ids ...int) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	var ps []fpl.Player
	for _, id := range ids {
		ps = append(ps, player(id, 60))
	}
	if err := s.UpsertPlayers(ctx, ps); err != nil {
		t.Fatal(err)
	}
}

func counts(t *testing.T, s *db.Store) db.NewsCounts {
	t.Helper()
	c, err := s.CountNews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.Newest = time.Time{} // checked by its own test; the others compare sizes
	return c
}

func TestStore_CountNewsReportsWhenTheNewestStoryWasPublished(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	withPlayers(t, s, 1)

	empty, err := s.CountNews(ctx)
	if err != nil || !empty.Newest.IsZero() {
		t.Fatalf("with no stories there is no newest: %v %v", empty.Newest, err)
	}

	older, newer := story("https://a/old", "Old", 0.1, 1), story("https://a/new", "New", 0.2, 1)
	older.PublishedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	newer.PublishedAt = time.Date(2026, 10, 21, 22, 51, 56, 0, time.UTC)
	if err := s.SaveNews(ctx, []news.Embedded{older, newer}); err != nil {
		t.Fatal(err)
	}

	got, err := s.CountNews(ctx)
	if err != nil || !got.Newest.Equal(newer.PublishedAt) {
		t.Errorf("newest = %v, want %v (err %v)", got.Newest, newer.PublishedAt, err)
	}
}

func TestStore_SaveNewsStoresArticleChunkVectorAndLinks(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	withPlayers(t, s, 1, 2)

	err := s.SaveNews(ctx, []news.Embedded{story("https://a/1", "Saka doubt", 0.5, 1, 2, 2)}) // 2 listed twice

	if err != nil {
		t.Fatal(err)
	}
	if got := counts(t, s); got != (db.NewsCounts{Articles: 1, Chunks: 1, PlayerLinks: 2}) {
		t.Errorf("counts = %+v, want 1 article, 1 chunk, 2 links (the duplicate id collapsed)", got)
	}

	var (
		content   string
		published time.Time
		emb       pgvector.Vector
	)
	err = pool.QueryRow(ctx, `SELECT c.content, a.published_at, c.embedding
		FROM news_chunks c JOIN news_articles a ON a.id = c.article_id`).Scan(&content, &published, &emb)
	if err != nil {
		t.Fatal(err)
	}
	if content != "Saka doubt. Some detail." {
		t.Errorf("chunk content = %q, want the item's full text", content)
	}
	if !published.Equal(time.Date(2026, 10, 21, 22, 51, 56, 0, time.UTC)) {
		t.Errorf("published_at = %v", published)
	}
	if got := emb.Slice(); len(got) != 1024 || got[0] != 0.5 {
		t.Errorf("embedding round trip: len %d first %v; want 1024 and 0.5", len(got), got[0])
	}
}

func TestStore_ExistingURLs(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.SaveNews(ctx, []news.Embedded{story("https://a/1", "One", 1), story("https://a/2", "Two", 2)}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExistingURLs(ctx, []string{"https://a/1", "https://a/3", "https://a/2"})

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["https://a/1"] || !got["https://a/2"] || got["https://a/3"] {
		t.Errorf("existing = %v, want exactly a/1 and a/2", got)
	}
	if none, err := s.ExistingURLs(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("empty lookup = %v, %v; want empty and no error", none, err)
	}
}

func TestStore_SaveNewsIsIdempotentPerURL(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.SaveNews(ctx, []news.Embedded{story("https://a/1", "One", 1)}); err != nil {
		t.Fatal(err)
	}

	// Saving the same URL again (a race between two runs) must not duplicate or fail.
	err := s.SaveNews(ctx, []news.Embedded{story("https://a/1", "One again", 9), story("https://a/2", "Two", 2)})

	if err != nil {
		t.Fatal(err)
	}
	if got := counts(t, s); got.Articles != 2 || got.Chunks != 2 {
		t.Errorf("counts = %+v, want 2 articles and 2 chunks", got)
	}
}

func TestStore_SaveNewsIsAtomic(t *testing.T) {
	tests := []struct {
		name    string
		bad     news.Embedded
		wantErr func(error) bool
	}{
		{
			"link to a player that does not exist",
			story("https://a/bad", "Bad", 1, 999),
			func(err error) bool {
				var pg *pgconn.PgError
				return errors.As(err, &pg) && pg.Code == "23503"
			},
		},
		{
			"embedding of the wrong size",
			news.Embedded{Item: news.Item{Source: "x", Title: "Bad", URL: "https://a/bad", PublishedAt: time.Now()}, Embedding: []float32{1, 2, 3}},
			func(err error) bool { return err != nil },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newStore(t)
			withPlayers(t, s, 1)

			err := s.SaveNews(context.Background(), []news.Embedded{story("https://a/good", "Good", 1, 1), tc.bad})

			if !tc.wantErr(err) {
				t.Fatalf("err = %v", err)
			}
			// The good item came first and must be rolled back with the bad one.
			if got := counts(t, s); got != (db.NewsCounts{}) {
				t.Errorf("counts after a failed save = %+v, want all zero", got)
			}
		})
	}
}

func TestStore_SaveNewsEdgeCases(t *testing.T) {
	s, _ := newStore(t)

	if err := s.SaveNews(context.Background(), nil); err != nil {
		t.Errorf("empty save: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SaveNews(ctx, []news.Embedded{story("https://a/1", "One", 1)}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled save err = %v, want context.Canceled", err)
	}
}

func TestStore_DeletingAnArticleCascadesToChunksAndLinks(t *testing.T) {
	s, pool := newStore(t)
	withPlayers(t, s, 1)
	if err := s.SaveNews(context.Background(), []news.Embedded{story("https://a/1", "One", 1, 1)}); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(context.Background(), `DELETE FROM news_articles`); err != nil {
		t.Fatal(err)
	}

	if got := counts(t, s); got != (db.NewsCounts{}) {
		t.Errorf("after deleting articles: %+v, want chunks and links gone too", got)
	}
}

func TestStore_PlayersUpdatedAtIsWhenAnyPlayerWasLastWritten(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	none, err := s.PlayersUpdatedAt(ctx)
	if err != nil || !none.IsZero() {
		t.Fatalf("with no players there is no time: %v %v", none, err)
	}

	withPlayers(t, s, 1)
	got, err := s.PlayersUpdatedAt(ctx)

	if err != nil || time.Since(got) > time.Minute || time.Since(got) < -time.Minute {
		t.Errorf("updated at %v (err %v): the upsert just wrote it", got, err)
	}
}
