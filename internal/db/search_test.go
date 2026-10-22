package db_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// axis returns a unit vector along dimension i. Distinct axes are orthogonal, so
// cosine similarity between them is exactly 0 and the expected ranking is exact.
func axis(i int) []float32 {
	v := make([]float32, 1024)
	v[i] = 1
	return v
}

// blend is mostly axis a with a little of axis b: close to a, further from b.
func blend(a, b int, wb float32) []float32 {
	v := make([]float32, 1024)
	v[a], v[b] = 1, wb
	return v
}

func at(day int) time.Time { return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC) }

func storyAt(url, title string, emb []float32, published time.Time, players ...int) news.Embedded {
	return news.Embedded{
		Item:      news.Item{Source: "bbc", Title: title, URL: url, PublishedAt: published, PlayerIDs: players},
		Embedding: emb,
	}
}

// searchFixture: three stories on three orthogonal topics, players 1 and 2.
//
//	a: axis 0, day 20, player 1     b: axis 1, day 21, player 2     c: axis 2, day 10, players 1+2
func searchFixture(t *testing.T) *db.Store {
	t.Helper()
	s, _ := newStore(t)
	withPlayers(t, s, 1, 2)
	err := s.SaveNews(context.Background(), []news.Embedded{
		storyAt("https://x/a", "A", axis(0), at(20), 1),
		storyAt("https://x/b", "B", axis(1), at(21), 2),
		storyAt("https://x/c", "C", axis(2), at(10), 1, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func titles(hits []db.Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Title)
	}
	return out
}

func TestSearchNews_RanksBySimilarity(t *testing.T) {
	s := searchFixture(t)

	// Closest to axis 1, then axis 0 (a little of it), then the orthogonal axis 2.
	hits, err := s.SearchNews(context.Background(), blend(1, 0, 0.5), db.SearchFilter{Limit: 10})

	if err != nil {
		t.Fatal(err)
	}
	if got := titles(hits); !slices.Equal(got, []string{"B", "A", "C"}) {
		t.Fatalf("order = %v, want [B A C]", got)
	}
	// Similarity is 1 for an identical direction, 0 for orthogonal.
	exact, err := s.SearchNews(context.Background(), axis(2), db.SearchFilter{Limit: 1})
	if err != nil || len(exact) != 1 {
		t.Fatalf("exact = %v, %v", exact, err)
	}
	if math.Abs(exact[0].Similarity-1) > 1e-6 {
		t.Errorf("similarity of identical vectors = %v, want 1", exact[0].Similarity)
	}
	if far := hits[2].Similarity; math.Abs(far) > 1e-6 {
		t.Errorf("similarity of orthogonal vectors = %v, want 0", far)
	}
	if hits[0].Similarity <= hits[1].Similarity {
		t.Errorf("hits are not ordered best first: %v then %v", hits[0].Similarity, hits[1].Similarity)
	}
}

func TestSearchNews_ReturnsEverythingACitationNeeds(t *testing.T) {
	s := searchFixture(t)

	hits, err := s.SearchNews(context.Background(), axis(2), db.SearchFilter{Limit: 1})

	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	h := hits[0]
	if h.Title != "C" || h.URL != "https://x/c" || h.Source != "bbc" || !h.PublishedAt.Equal(at(10)) || h.Content != "C" {
		t.Errorf("hit = %+v", h)
	}
	if !slices.Equal(h.PlayerIDs, []int{1, 2}) {
		t.Errorf("PlayerIDs = %v, want [1 2]", h.PlayerIDs)
	}
}

func TestSearchNews_Filters(t *testing.T) {
	s := searchFixture(t)
	q := blend(0, 1, 0.5) // prefers A, then B, then C

	tests := []struct {
		name   string
		filter db.SearchFilter
		want   []string
	}{
		{"no filter", db.SearchFilter{Limit: 10}, []string{"A", "B", "C"}},
		{"limit", db.SearchFilter{Limit: 2}, []string{"A", "B"}},
		{"one player", db.SearchFilter{PlayerIDs: []int{2}, Limit: 10}, []string{"B", "C"}},
		{"any of several players", db.SearchFilter{PlayerIDs: []int{1, 2}, Limit: 10}, []string{"A", "B", "C"}},
		{"unknown player", db.SearchFilter{PlayerIDs: []int{999}, Limit: 10}, nil},
		{"since drops older", db.SearchFilter{Since: at(15), Limit: 10}, []string{"A", "B"}},
		{"since is inclusive", db.SearchFilter{Since: at(20), Limit: 10}, []string{"A", "B"}},
		{"since after everything", db.SearchFilter{Since: at(30), Limit: 10}, nil},
		{"player and recency together", db.SearchFilter{PlayerIDs: []int{1}, Since: at(15), Limit: 10}, []string{"A"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := s.SearchNews(context.Background(), q, tc.filter)

			if err != nil {
				t.Fatal(err)
			}
			if got := titles(hits); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchNews_AStoryLinkedToTwoMatchingPlayersAppearsOnce(t *testing.T) {
	s := searchFixture(t)

	// C is linked to both players; a join-based filter would return it twice.
	hits, err := s.SearchNews(context.Background(), axis(2), db.SearchFilter{PlayerIDs: []int{1, 2}, Limit: 10})

	if err != nil {
		t.Fatal(err)
	}
	if n := slices.Index(titles(hits), "C"); n < 0 || slices.Index(titles(hits)[n+1:], "C") >= 0 {
		t.Errorf("titles = %v, want C exactly once", titles(hits))
	}
}

func fplNote(playerID int, published time.Time, emb []float32) news.Embedded {
	return news.Embedded{
		Item: news.Item{
			Source: news.SourceFPL, Title: "note", URL: fmt.Sprintf("fpl://player/%d/%s", playerID, published.Format(time.RFC3339)),
			PublishedAt: published, PlayerIDs: []int{playerID},
		},
		Embedding: emb,
	}
}

func TestSearchNews_OnlyAPlayersCurrentFPLNoteCounts(t *testing.T) {
	// FPL notes are state, not events: a note holds until FPL replaces or clears
	// it. Returning a superseded note would tell the user a recovered player is
	// still injured.
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	current := at(20)
	injured, recovered := player(1, 60), player(2, 60)
	injured.News, injured.NewsAdded = "Knee injury", &current // the current note is the day-20 one
	recovered.News, recovered.NewsAdded = "", nil             // FPL cleared his note
	if err := s.UpsertPlayers(ctx, []fpl.Player{injured, recovered}); err != nil {
		t.Fatal(err)
	}
	err := s.SaveNews(ctx, []news.Embedded{
		fplNote(1, at(5), axis(0)),                         // superseded by the day-20 note
		fplNote(1, at(20), axis(1)),                        // current
		fplNote(2, at(8), axis(2)),                         // player 2 has since recovered
		storyAt("https://x/bbc", "BBC", axis(3), at(3), 2), // other sources are events and always stay
	})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.SearchNews(ctx, axis(0), db.SearchFilter{Limit: 10})

	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, h := range hits {
		urls = append(urls, h.URL)
	}
	slices.Sort(urls)
	want := []string{"fpl://player/1/" + at(20).Format(time.RFC3339), "https://x/bbc"}
	if !slices.Equal(urls, want) {
		t.Errorf("urls = %v, want only the current FPL note and the BBC story %v", urls, want)
	}
}

func TestSearchNews_EdgeCases(t *testing.T) {
	s := searchFixture(t)

	if hits, err := s.SearchNews(context.Background(), axis(0), db.SearchFilter{Limit: 0}); err != nil || len(hits) != 0 {
		t.Errorf("limit 0 = %v, %v; want nothing", hits, err)
	}
	if _, err := s.SearchNews(context.Background(), []float32{1, 2, 3}, db.SearchFilter{Limit: 5}); err == nil {
		t.Error("want an error for a query vector of the wrong size")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SearchNews(ctx, axis(0), db.SearchFilter{Limit: 5}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled search err = %v, want context.Canceled", err)
	}

	empty, _ := newStore(t)
	if hits, err := empty.SearchNews(context.Background(), axis(0), db.SearchFilter{Limit: 5}); err != nil || len(hits) != 0 {
		t.Errorf("empty database = %v, %v; want nothing and no error", hits, err)
	}
}

func TestStore_FindPlayersByName(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.UpsertTeams(ctx, arsenal()); err != nil {
		t.Fatal(err)
	}
	saka, sakaTwin, other := player(1, 60), player(2, 60), player(3, 60)
	saka.WebName, saka.FirstName, saka.LastName = "Saka", "Bukayo", "Saka"
	sakaTwin.WebName, sakaTwin.FirstName, sakaTwin.LastName = "B.Fernandes", "Bruno", "Fernandes Borges"
	other.WebName, other.FirstName, other.LastName = "Haaland", "Erling", "Haaland"
	if err := s.UpsertPlayers(ctx, []fpl.Player{saka, sakaTwin, other}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query string
		want  []int
	}{
		{"saka", []int{1}},
		{"SAKA", []int{1}},
		{"Bukayo Saka", []int{1}},
		{"B.Fernandes", []int{2}},
		{"haaland", []int{3}},
		{"nobody", nil},
		{"", nil},
	}
	for _, tc := range tests {
		got, err := s.FindPlayers(ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("FindPlayers(%q) = %v, want %v", tc.query, ids, tc.want)
		}
	}
}
