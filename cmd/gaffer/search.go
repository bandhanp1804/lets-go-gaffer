package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

const searchUsage = `usage: gaffer search [-k N] [-player NAME] [-days N] QUERY...
  -k       number of results (default 5)
  -player  only stories about this player (exact name, e.g. Saka)
  -days    only stories from the last N days (default: any age)
flags must come before the query`

// runSearch runs a semantic search over stored news and prints the hits. It is
// the way to judge retrieval quality directly, before any LLM is involved.
func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	k := fs.Int("k", 5, "")
	player := fs.String("player", "", "")
	days := fs.Int("days", 0, "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, searchUsage)
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" || *k < 1 || *days < 0 {
		return errors.New(searchUsage)
	}

	dsn, key := os.Getenv("GAFFER_DSN"), os.Getenv("VOYAGE_API_KEY")
	if dsn == "" {
		return errors.New("GAFFER_DSN is not set")
	}
	if key == "" {
		return errors.New("VOYAGE_API_KEY is not set (add it to .env)")
	}
	model := os.Getenv("VOYAGE_MODEL")
	if model == "" {
		model = "voyage-4"
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	store := db.NewStore(pool)

	filter := db.SearchFilter{Limit: *k}
	if *player != "" {
		matches, err := store.FindPlayers(ctx, *player)
		if err != nil {
			return err
		}
		teams, err := store.ListTeams(ctx)
		if err != nil {
			return err
		}
		if filter.PlayerIDs, err = pickPlayer(*player, matches, teams); err != nil {
			return err
		}
	}
	if *days > 0 {
		filter.Since = time.Now().Add(-time.Duration(*days) * 24 * time.Hour)
	}

	vc, err := voyageClient()
	if err != nil {
		return err
	}
	embedder, err := embed.NewVoyage(vc, embed.VoyageConfig{APIKey: key, Model: model, Dimensions: embeddingDims, BatchSize: 32})
	if err != nil {
		return err
	}
	// A search query is embedded as a Query, not a Document: Voyage embeds them asymmetrically.
	vecs, err := embedder.Embed(ctx, []string{query}, embed.Query)
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}

	hits, err := store.SearchNews(ctx, vecs[0], filter)
	if err != nil {
		return err
	}
	players, err := store.ListPlayers(ctx)
	if err != nil {
		return err
	}
	printHits(os.Stdout, hits, players, time.Now())
	return nil
}

// pickPlayer resolves a --player match to ids, refusing when it is missing or
// ambiguous rather than silently filtering to the wrong player.
func pickPlayer(name string, matches []fpl.Player, teams []fpl.Team) ([]int, error) {
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no player named %q (use the FPL web name, e.g. Saka or B.Fernandes)", name)
	case 1:
		return []int{matches[0].ID}, nil
	}
	short := make(map[int]string, len(teams))
	for _, t := range teams {
		short[t.ID] = t.ShortName
	}
	var opts []string
	for _, p := range matches {
		opts = append(opts, fmt.Sprintf("%s (%s) as %q", p.WebName, short[p.TeamID], p.WebName))
	}
	return nil, fmt.Errorf("%q matches %d players: %s; use the exact web name", name, len(matches), strings.Join(opts, ", "))
}

func printHits(w io.Writer, hits []db.Hit, players []fpl.Player, now time.Time) {
	if len(hits) == 0 {
		fmt.Fprintln(w, "no matching stories")
		return
	}
	web := make(map[int]string, len(players))
	for _, p := range players {
		web[p.ID] = p.WebName
	}
	for i, h := range hits {
		var names []string
		for _, id := range h.PlayerIDs {
			names = append(names, web[id])
		}
		about := ""
		if len(names) > 0 {
			about = "  about: " + strings.Join(names, ", ")
		}
		fmt.Fprintf(w, "%d. [%.2f] %s ago  %s%s\n", i+1, h.Similarity, age(now, h.PublishedAt), h.Source, about)
		fmt.Fprintf(w, "   %s\n", truncate(h.Content, 230))
		fmt.Fprintf(w, "   %s\n\n", h.URL)
	}
}

// age renders how long before now t was, coarsely: "5m", "3h", "2d".
func age(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
