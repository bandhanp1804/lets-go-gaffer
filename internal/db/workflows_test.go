package db_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

func TestStore_AlertsArePerInbox(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	withPlayers(t, s, 7)

	first, err := s.AddAlert(ctx, db.Alert{Kind: "watch", Title: "Saka: doubtful", PlayerID: 7, URL: "https://x.test/a"}) // the owners'
	if err != nil {
		t.Fatal(err)
	}
	second, _ := s.AddAlert(ctx, db.Alert{Kind: "health", Title: "Eval at 80%"})
	visitors, _ := s.AddAlert(ctx, db.Alert{User: "visitor@x.com", Kind: "watch", Title: "Saka in the news", PlayerID: 7})

	owners, unread, err := s.ListAlerts(ctx, "", 10)
	if err != nil || unread != 2 || len(owners) != 2 || owners[0].ID != second || owners[1].PlayerID != 7 || owners[1].URL != "https://x.test/a" {
		t.Fatalf("owners' inbox = %+v unread=%d err=%v", owners, unread, err)
	}
	mine, unread, _ := s.ListAlerts(ctx, "visitor@x.com", 10)
	if unread != 1 || len(mine) != 1 || mine[0].ID != visitors || mine[0].User != "visitor@x.com" {
		t.Fatalf("visitor's inbox = %+v unread=%d", mine, unread)
	}

	// Marking read reaches only the inbox it is asked of, even with an id range that spans others.
	if err := s.MarkAlertsRead(ctx, "visitor@x.com", visitors); err != nil {
		t.Fatal(err)
	}
	if _, unread, _ := s.ListAlerts(ctx, "", 10); unread != 2 {
		t.Errorf("the owners' alerts must stay unread: %d", unread)
	}
	if err := s.MarkAlertsRead(ctx, "", first); err != nil {
		t.Fatal(err)
	}
	owners, unread, _ = s.ListAlerts(ctx, "", 10)
	if unread != 1 || owners[0].Read || !owners[1].Read {
		t.Errorf("only alerts up to the id are read: unread=%d %+v", unread, owners)
	}
	if one, _, _ := s.ListAlerts(ctx, "", 1); len(one) != 1 {
		t.Errorf("limit: %d", len(one))
	}
}

func TestStore_WatchlistsArePerUser(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	withPlayers(t, s, 3, 9)

	for _, w := range []struct {
		user string
		id   int
	}{{"", 9}, {"", 3}, {"", 9}, {"visitor@x.com", 9}} {
		if err := s.Watch(ctx, w.user, w.id); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Watchlist(ctx, ""); !slices.Equal(got, []int{9, 3}) {
		t.Errorf("owners' watchlist = %v, want [9 3]", got)
	}
	if got, _ := s.Watchlist(ctx, "visitor@x.com"); !slices.Equal(got, []int{9}) {
		t.Errorf("visitor's watchlist = %v", got)
	}
	if got, _ := s.WatchedPlayers(ctx); !slices.Equal(got, []int{3, 9}) {
		t.Errorf("watched by anyone = %v", got)
	}
	if got, _ := s.Watchers(ctx, 9); !slices.Equal(got, []string{"", "visitor@x.com"}) {
		t.Errorf("watchers of 9 = %v", got)
	}

	left, err := s.Unwatch(ctx, "", 9)
	if err != nil || left != 1 {
		t.Errorf("after the owners stop watching 9, the visitor still does: left=%d err=%v", left, err)
	}
	if left, _ := s.Unwatch(ctx, "visitor@x.com", 9); left != 0 {
		t.Errorf("nobody watches 9 now: %d", left)
	}
	if left, err := s.Unwatch(ctx, "nobody", 42); err != nil || left != 0 {
		t.Errorf("unwatching someone not watched: %d %v", left, err)
	}
	if err := s.Watch(ctx, "", 42); err == nil {
		t.Error("watching a player who does not exist should fail")
	}
}

func TestStore_EvalAndBacktestRunsRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	if err := s.SaveEvalRun(ctx, db.EvalRun{Model: "haiku", PassRate: 0.95, Runs: 25, Tokens: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEvalRun(ctx, db.EvalRun{Model: "haiku", PassRate: 0.8, Runs: 25, Failing: []string{"captain", "wildcard"}}); err != nil {
		t.Fatal(err)
	}
	evals, err := s.EvalRuns(ctx, 10)
	if err != nil || len(evals) != 2 || evals[0].PassRate != 0.8 || !slices.Equal(evals[0].Failing, []string{"captain", "wildcard"}) || len(evals[1].Failing) != 0 {
		t.Errorf("evals = %+v (%v)", evals, err)
	}

	if err := s.SaveBacktestRuns(ctx, []db.BacktestRun{
		{ThroughGW: 7, Model: "blend", Spearman: 0.41, MAE: 1.9, Top20: 5.1},
		{ThroughGW: 7, Model: "form", Spearman: 0.30, MAE: 2.1, Top20: 4.2},
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.SaveBacktestRuns(ctx, []db.BacktestRun{{ThroughGW: 8, Model: "blend", Spearman: 0.39, MAE: 2.0, Top20: 5.0}})
	got, err := s.BacktestRuns(ctx, "blend", 10)
	if err != nil || len(got) != 2 || got[0].ThroughGW != 8 || got[1].Spearman != 0.41 {
		t.Errorf("blend runs = %+v (%v)", got, err)
	}
}

func TestStore_MarkBriefingSentOnlyOnce(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	first, err := s.MarkBriefingSent(ctx, 9)
	again, _ := s.MarkBriefingSent(ctx, 9)
	other, _ := s.MarkBriefingSent(ctx, 10)

	if err != nil || !first || again || !other {
		t.Errorf("first=%v again=%v other=%v err=%v", first, again, other, err)
	}
	if err := s.ReleaseBriefing(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if retry, _ := s.MarkBriefingSent(ctx, 9); !retry {
		t.Error("a released briefing can be marked again")
	}
}

func TestStore_PlayerStates(t *testing.T) {
	s, _ := newStore(t)
	withPlayers(t, s, 1, 2)

	got, err := s.PlayerStates(context.Background())

	if err != nil || len(got) != 2 || got[1].Status != "a" || got[2].News != "" {
		t.Errorf("states = %+v (%v)", got, err)
	}
}

func TestStore_RelinkNewsPageWalksTheChunksInPages(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	withPlayers(t, s, 1)
	var items []news.Embedded
	for i := range 5 {
		items = append(items, story(fmt.Sprintf("https://x.test/%d", i), fmt.Sprintf("Story %d", i), 0.1))
	}
	if err := s.SaveNews(ctx, items); err != nil {
		t.Fatal(err)
	}
	link := func(string) []int { return []int{1} }

	var pages []db.RelinkPage
	for after := int64(0); ; {
		p, err := s.RelinkNewsPage(ctx, after, 2, link)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, p)
		if p.Done {
			break
		}
		after = p.Next
	}

	if len(pages) != 3 || pages[0].Changed != 2 || pages[2].Changed != 1 || !pages[2].Done {
		t.Errorf("pages = %+v, want 2+2+1 and done", pages)
	}
	if c := counts(t, s); c.PlayerLinks != 5 {
		t.Errorf("links = %d, want 5", c.PlayerLinks)
	}
}

func TestStore_ChunksAfterAndUpdateEmbeddings(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.SaveNews(ctx, []news.Embedded{story("https://x.test/a", "A", 0.1), story("https://x.test/b", "B", 0.2)}); err != nil {
		t.Fatal(err)
	}

	first, err := s.ChunksAfter(ctx, 0, 1)
	if err != nil || len(first) != 1 || !strings.HasPrefix(first[0].Content, "A") {
		t.Fatalf("first page = %+v (%v)", first, err)
	}
	rest, _ := s.ChunksAfter(ctx, first[0].ID, 10)
	if len(rest) != 1 || !strings.HasPrefix(rest[0].Content, "B") {
		t.Fatalf("second page = %+v", rest)
	}

	// Point A along the second axis; a query along it should now find A first.
	v := make([]float32, 1024)
	v[1] = 1
	if err := s.UpdateEmbeddings(ctx, map[int64][]float32{first[0].ID: v}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchNews(ctx, v, db.SearchFilter{Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].Title != "A" {
		t.Errorf("hits = %+v (%v)", hits, err)
	}
}
