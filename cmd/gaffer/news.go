package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// embeddingDims must match the vector(1024) column in migrations/0001.
const embeddingDims = 1024

// feeds are the RSS sources. Premier League's own site publishes no RSS, so its
// news comes from FPL's official per-player notes instead (see news.PlayerNewsItems).
// The Athletic is left out: its feed answers 403 and its stories are paywalled.
var feeds = []struct{ name, url string }{
	{"bbc-premier-league", "https://feeds.bbci.co.uk/sport/football/premier-league/rss.xml"},
	{"sky-football-news", "https://www.skysports.com/rss/12040"},
	{"sky-football", "https://www.skysports.com/rss/11095"},
	{"talksport-football", "https://talksport.com/football/feed/"},
	{"guardian-premier-league", "https://www.theguardian.com/football/premierleague/rss"},
}

// politeClient returns an HTTP client that spaces requests by interval and
// retries transient failures. Retry wraps the limiter so retries are paced too.
// There is no http.Client.Timeout: it would cut off backoff, so callers bound
// the work with a context instead.
func politeClient(interval time.Duration) *http.Client {
	return politeClientWith(interval, httpx.RetryConfig{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second})
}

func politeClientWith(interval time.Duration, retry httpx.RetryConfig) *http.Client {
	return &http.Client{Transport: httpx.NewRetryTransport(
		httpx.NewRateLimitTransport(http.DefaultTransport, httpx.NewLimiter(interval)),
		retry,
	)}
}

// voyageClient paces requests to Voyage's limit. Without a payment method on the
// account that is 3 requests/minute and 10K tokens/minute; set VOYAGE_RPM higher
// once the account has standard limits. The extra second absorbs clock skew
// between us and Voyage's window. Retries back off for a long time because a
// 429 means waiting out part of a minute, not milliseconds.
func voyageClient() (*http.Client, error) {
	rpm := 3
	if v := os.Getenv("VOYAGE_RPM"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("VOYAGE_RPM must be a positive integer, got %q", v)
		}
		rpm = n
	}
	return politeClientWith(time.Minute/time.Duration(rpm)+time.Second, httpx.RetryConfig{
		MaxAttempts: 5, BaseDelay: 15 * time.Second, MaxDelay: time.Minute,
	}), nil
}

// runNews refreshes player data, then pulls news from RSS and FPL, embeds what
// is new, and stores it for semantic search.
func runNews(ctx context.Context) error {
	dsn := os.Getenv("GAFFER_DSN")
	if dsn == "" {
		return errors.New("GAFFER_DSN is not set")
	}
	key := os.Getenv("VOYAGE_API_KEY")
	if key == "" {
		return errors.New("VOYAGE_API_KEY is not set (add it to .env)")
	}
	model := os.Getenv("VOYAGE_MODEL")
	if model == "" {
		model = "voyage-4"
	}
	migrations := os.Getenv("GAFFER_MIGRATIONS")
	if migrations == "" {
		migrations = "migrations"
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, os.DirFS(migrations)); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	store := db.NewStore(pool)

	// One API call refreshes players (and FPL's news on them) without the
	// minute-long history crawl.
	boot, err := ingest.Bootstrap(ctx, fpl.NewHTTPClient(politeClient(100*time.Millisecond)), store)
	if err != nil {
		return err
	}

	feedClient := politeClient(500 * time.Millisecond)
	sources := make([]news.Source, 0, len(feeds)+1)
	for _, f := range feeds {
		sources = append(sources, news.NewRSSSource(f.name, f.url, feedClient))
	}
	sources = append(sources, news.NewPlayerNewsSource(news.PlayerNewsItems(boot.Players, boot.Teams)))

	vc, err := voyageClient()
	if err != nil {
		return err
	}
	// 32 short texts is about 2K tokens, so even three requests a minute stay
	// well under the 10K tokens/minute free-account limit.
	embedder, err := embed.NewVoyage(vc, embed.VoyageConfig{
		APIKey: key, Model: model, Dimensions: embeddingDims, BatchSize: 32,
	})
	if err != nil {
		return err
	}

	start := time.Now()
	sum, err := news.Ingest(ctx, sources, news.NewLinker(boot.Players, boot.Teams), embedder, store)

	slog.Info("news ingest finished",
		"fetched", sum.Fetched, "new", sum.New, "duplicates", sum.Duplicates, "linked_to_players", sum.Linked,
		"elapsed", time.Since(start).Round(time.Millisecond))
	names := make([]string, 0, len(sum.SourceErrors))
	for name := range sum.SourceErrors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		slog.Warn("source failed", "source", name, "err", sum.SourceErrors[name])
	}
	if err != nil {
		return err
	}
	if c, cerr := store.CountNews(ctx); cerr == nil {
		slog.Info("news stored", "articles", c.Articles, "chunks", c.Chunks, "player_links", c.PlayerLinks)
	}
	return nil
}
