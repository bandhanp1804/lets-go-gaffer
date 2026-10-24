package news

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
)

// ChunkSize is how many items are embedded and saved together.
const ChunkSize = 64

// Source produces news items, from a feed or anywhere else.
type Source interface {
	Name() string
	Items(ctx context.Context) ([]Item, error)
}

// Embedder is satisfied by embed.Voyage and embed.Hashing.
type Embedder interface {
	Embed(ctx context.Context, texts []string, kind embed.InputType) ([][]float32, error)
}

// Store is satisfied by db.Store.
type Store interface {
	// ExistingURLs reports which of urls are already stored.
	ExistingURLs(ctx context.Context, urls []string) (map[string]bool, error)
	// SaveNews stores items, their vectors, and their player links atomically.
	SaveNews(ctx context.Context, items []Embedded) error
}

// Summary reports what one ingest run did.
type Summary struct {
	Fetched      int // items returned by all sources
	Duplicates   int // dropped: repeated across sources, or already stored
	New          int // embedded and saved
	Linked       int // of New, how many mention at least one player
	SourceErrors map[string]error
}

// FailedSources names the sources that failed, sorted.
func (s Summary) FailedSources() []string {
	names := make([]string, 0, len(s.SourceErrors))
	for n := range s.SourceErrors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Items from a PlayerNewsSource are fixed, so Name and Items are trivial.
func (s *PlayerNewsSource) Name() string { return SourceFPL }

func (s *PlayerNewsSource) Items(ctx context.Context) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.items, nil
}

// Ingest fetches every source concurrently, drops anything already stored, links
// the rest to players, embeds them as documents, and saves them.
//
// One failing source does not stop the others; Ingest only fails outright when
// every source fails or a later stage (lookup, embedding, saving) does. New items
// are embedded and saved in chunks of ChunkSize, each chunk atomically, so a
// failure keeps the chunks already saved. A stored URL is never embedded again,
// which keeps repeated runs nearly free and lets a failed run resume.
//
// Items are processed in source order, then feed order, whatever order the
// sources finish in. linker may be nil to skip linking.
func Ingest(ctx context.Context, sources []Source, linker *Linker, e Embedder, s Store) (Summary, error) {
	sum := Summary{SourceErrors: map[string]error{}}

	results := make([][]Item, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = src.Items(ctx)
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return sum, fmt.Errorf("news ingest: %w", err)
	}
	var failures []error
	for i, err := range errs {
		if err != nil {
			sum.SourceErrors[sources[i].Name()] = err
			failures = append(failures, fmt.Errorf("source %s: %w", sources[i].Name(), err))
		}
	}
	if len(sources) > 0 && len(failures) == len(sources) {
		return sum, fmt.Errorf("news ingest: every source failed: %w", errors.Join(failures...))
	}

	fresh, dups := Merge(results)
	sum.Fetched += len(fresh) + dups
	sum.Duplicates += dups
	todo, stored, err := DropStored(ctx, s, fresh)
	if err != nil {
		return sum, fmt.Errorf("news ingest: %w", err)
	}
	sum.Duplicates += stored
	if len(todo) == 0 {
		return sum, nil
	}
	LinkAll(linker, todo)

	// Embed and save one chunk at a time. Embedding APIs rate-limit and fail
	// mid-run; saving per chunk keeps the work already paid for, and because
	// stored URLs are skipped next time, a rerun resumes where this one stopped.
	for start := 0; start < len(todo); start += ChunkSize {
		chunk := todo[start:min(start+ChunkSize, len(todo))]
		if err := EmbedAndSave(ctx, chunk, e, s); err != nil {
			return sum, fmt.Errorf("news ingest: items %d-%d of %d: %w", start, start+len(chunk)-1, len(todo), err)
		}
		sum.New += len(chunk)
		sum.Linked += Linked(chunk)
		slog.Info("saved news", "saved", sum.New, "of", len(todo))
	}
	return sum, nil
}

// Merge joins the sources' items in source order, keeping the first copy of each story, and
// says how many copies it dropped. A story is the same if it has the same URL, or the same
// title from the same publisher: Sky lists one article in several feeds under URLs that differ
// only by section.
func Merge(results [][]Item) (fresh []Item, duplicates int) {
	seenURL := map[string]bool{}
	seenStory := map[string]bool{}
	for _, items := range results {
		for _, it := range items {
			story := storyKey(it)
			if seenURL[it.URL] || seenStory[story] {
				duplicates++
				continue
			}
			seenURL[it.URL], seenStory[story] = true, true
			fresh = append(fresh, it)
		}
	}
	return fresh, duplicates
}

// DropStored returns the items not stored yet, and how many were.
func DropStored(ctx context.Context, s Store, items []Item) (todo []Item, stored int, err error) {
	if len(items) == 0 {
		return nil, 0, nil
	}
	urls := make([]string, len(items))
	for i, it := range items {
		urls[i] = it.URL
	}
	have, err := s.ExistingURLs(ctx, urls)
	if err != nil {
		return nil, 0, fmt.Errorf("check stored items: %w", err)
	}
	for _, it := range items {
		if have[it.URL] {
			stored++
			continue
		}
		todo = append(todo, it)
	}
	return todo, stored, nil
}

// LinkAll fills in the players of every item that does not name them already. A nil linker
// links nothing.
func LinkAll(linker *Linker, items []Item) {
	if linker == nil {
		return
	}
	for i := range items {
		if len(items[i].PlayerIDs) == 0 {
			items[i].PlayerIDs = linker.Link(items[i].Text())
		}
	}
}

// Linked counts the items that mention at least one player.
func Linked(items []Item) int {
	n := 0
	for _, it := range items {
		if len(it.PlayerIDs) > 0 {
			n++
		}
	}
	return n
}

// storyKey identifies a story by publisher host and normalised title.
func storyKey(it Item) string {
	host := ""
	if u, err := url.Parse(it.URL); err == nil {
		host = strings.ToLower(u.Host)
	}
	return host + "|" + strings.ToLower(strings.Join(strings.Fields(it.Title), " "))
}

// EmbedAndSave embeds one chunk of items (up to ChunkSize) and stores them together.
func EmbedAndSave(ctx context.Context, chunk []Item, e Embedder, s Store) error {
	texts := make([]string, len(chunk))
	for i, it := range chunk {
		texts[i] = it.Text()
	}
	vecs, err := e.Embed(ctx, texts, embed.Document)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != len(chunk) {
		return fmt.Errorf("embedder returned %d vectors for %d items", len(vecs), len(chunk))
	}
	out := make([]Embedded, len(chunk))
	for i := range chunk {
		out[i] = Embedded{Item: chunk[i], Embedding: vecs[i]}
	}
	if err := s.SaveNews(ctx, out); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}
