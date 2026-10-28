package workflows

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// NewsInput names the feeds to pull. The worker fills it from its feed list, so a workflow
// never reads configuration itself.
type NewsInput struct {
	Feeds []string
}

// NewsResult is what one pull did.
type NewsResult struct {
	Fetched     int      `json:"fetched"`
	New         int      `json:"new"`
	Duplicates  int      `json:"duplicates"`
	Linked      int      `json:"linked"`
	FailedFeeds []string `json:"failed_feeds"`
}

// StatusChange is a watched player whose FPL status or note changed in this pull.
type StatusChange struct {
	PlayerID int
	Name     string
	Status   string // as it reads: "doubtful (75%)"
	News     string
}

// PlayersRefreshed is what RefreshPlayers found: FPL's notes as news items, and the watched
// players whose status changed.
type PlayersRefreshed struct {
	FPLItems []news.Item
	Changes  []StatusChange
}

// Prepared is the pull after merging and dropping what is stored: the items to embed, how many
// were duplicates, and who is watched.
type Prepared struct {
	Todo       []news.Item
	Fetched    int
	Duplicates int
	Watched    []int
}

// NewsRefresh pulls every feed and FPL's notes, embeds what is new, and tells the watched
// players' workflows about their news.
//
// Each feed is its own activity, so one feed's outage is retried on its own and, if it stays
// down, recorded without stopping the rest. Embedding runs one chunk per activity with a
// patient retry policy: a rate-limited key (HTTP 429) waits and tries again, and the chunks
// already saved stay saved. Vectors never leave the activity, so they are not in the history.
func NewsRefresh(ctx workflow.Context, in NewsInput) (NewsResult, error) {
	var a *Activities
	res := NewsResult{FailedFeeds: []string{}}

	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var players PlayersRefreshed
	if err := workflow.ExecuteActivity(quick, a.RefreshPlayers).Get(ctx, &players); err != nil {
		return res, fmt.Errorf("refresh players: %w", err)
	}

	futures := make([]workflow.Future, len(in.Feeds))
	for i, name := range in.Feeds {
		futures[i] = workflow.ExecuteActivity(quick, a.FetchFeed, name)
	}
	var results [][]news.Item
	for i, f := range futures {
		var items []news.Item
		if err := f.Get(ctx, &items); err != nil {
			workflow.GetLogger(ctx).Warn("feed failed", "feed", in.Feeds[i], "err", err)
			res.FailedFeeds = append(res.FailedFeeds, in.Feeds[i])
			continue
		}
		results = append(results, items)
	}
	results = append(results, players.FPLItems)

	var prep Prepared
	if err := workflow.ExecuteActivity(quick, a.PrepareNews, results).Get(ctx, &prep); err != nil {
		return res, fmt.Errorf("prepare news: %w", err)
	}
	res.Fetched, res.Duplicates = prep.Fetched, prep.Duplicates

	embed := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 20 * time.Second, BackoffCoefficient: 2, MaximumInterval: 2 * time.Minute, MaximumAttempts: 6,
		},
	})
	var saved []news.Item
	for start := 0; start < len(prep.Todo); start += news.ChunkSize {
		chunk := prep.Todo[start:min(start+news.ChunkSize, len(prep.Todo))]
		if err := workflow.ExecuteActivity(embed, a.EmbedAndSave, chunk).Get(ctx, nil); err != nil {
			signalWatchers(ctx, prep.Watched, saved, players.Changes)
			return res, fmt.Errorf("embed items %d-%d: %w", start, start+len(chunk)-1, err)
		}
		res.New += len(chunk)
		res.Linked += news.Linked(chunk)
		saved = append(saved, chunk...)
	}
	signalWatchers(ctx, prep.Watched, saved, players.Changes)
	return res, nil
}

// signalWatchers tells each watched player's workflow about his new stories and status change.
// A player whose workflow is not running (no worker started it yet) is skipped: the inbox is
// fed by his workflow, not by this one.
func signalWatchers(ctx workflow.Context, watched []int, saved []news.Item, changes []StatusChange) {
	for _, c := range changes {
		send(ctx, c.PlayerID, SignalStatus, StatusSignal{Name: c.Name, Status: c.Status, News: c.News})
	}
	for _, it := range saved {
		if it.Source == news.SourceFPL { // a note is a status change, already sent above
			continue
		}
		for _, id := range it.PlayerIDs {
			if slices.Contains(watched, id) {
				send(ctx, id, SignalNews, NewsSignal{Title: it.Title, URL: it.URL, Source: it.Source})
			}
		}
	}
}

func send(ctx workflow.Context, playerID int, signal string, arg any) {
	err := workflow.SignalExternalWorkflow(ctx, WatchID(playerID), "", signal, arg).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Info("watch workflow not signalled", "player", playerID, "err", err)
	}
}

// WatchID is the workflow id of a player's watch.
func WatchID(playerID int) string { return "watch-" + strconv.Itoa(playerID) }

// RefreshPlayers downloads the season (prices, availability, FPL's notes), stores it, and
// reports which watched players' status or note changed since the last download.
func (a *Activities) RefreshPlayers(ctx context.Context) (PlayersRefreshed, error) {
	before, err := a.Store.PlayerStates(ctx)
	if err != nil {
		return PlayersRefreshed{}, err
	}
	watched, err := a.Store.WatchedPlayers(ctx)
	if err != nil {
		return PlayersRefreshed{}, err
	}
	boot, err := ingest.Bootstrap(ctx, a.FPL, a.Store)
	if err != nil {
		return PlayersRefreshed{}, err
	}
	out := PlayersRefreshed{FPLItems: news.PlayerNewsItems(boot.Players, boot.Teams)}
	for _, p := range boot.Players {
		old, known := before[p.ID]
		if !known || !slices.Contains(watched, p.ID) || (old.Status == p.Status && old.News == p.News) {
			continue
		}
		out.Changes = append(out.Changes, StatusChange{PlayerID: p.ID, Name: p.WebName, Status: service.StatusText(p), News: p.News})
	}
	return out, nil
}

// FetchFeed reads one feed.
func (a *Activities) FetchFeed(ctx context.Context, name string) ([]news.Item, error) {
	src, ok := a.Feeds[name]
	if !ok {
		return nil, permanent("no feed is called %q", name)
	}
	return src.Items(ctx)
}

// PrepareNews merges the feeds, drops what is stored, and links the rest to players.
func (a *Activities) PrepareNews(ctx context.Context, results [][]news.Item) (Prepared, error) {
	fresh, dups := news.Merge(results)
	p := Prepared{Fetched: len(fresh) + dups, Duplicates: dups}
	todo, stored, err := news.DropStored(ctx, a.Store, fresh)
	if err != nil {
		return Prepared{}, err
	}
	p.Duplicates += stored
	if len(todo) > 0 {
		players, err := a.Store.ListPlayers(ctx)
		if err != nil {
			return Prepared{}, err
		}
		teams, err := a.Store.ListTeams(ctx)
		if err != nil {
			return Prepared{}, err
		}
		news.LinkAll(news.NewLinker(players, teams), todo)
	}
	p.Todo = todo
	if p.Watched, err = a.Store.WatchedPlayers(ctx); err != nil {
		return Prepared{}, err
	}
	return p, nil
}

// EmbedAndSave embeds one chunk and stores it.
func (a *Activities) EmbedAndSave(ctx context.Context, chunk []news.Item) error {
	if a.Embedder == nil {
		return permanent("VOYAGE_API_KEY is not set, so stories cannot be embedded")
	}
	return news.EmbedAndSave(ctx, chunk, a.Embedder, a.Store)
}
