package news

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

	// Merge in source order, keeping the first copy of each story. A story is the
	// same if it has the same URL, or the same title from the same publisher: Sky
	// lists one article in several feeds under URLs that differ only by section.
	seenURL := map[string]bool{}
	seenStory := map[string]bool{}
	var fresh []Item
	for _, items := range results {
		for _, it := range items {
			sum.Fetched++
			story := storyKey(it)
			if seenURL[it.URL] || seenStory[story] {
				sum.Duplicates++
				continue
			}
			seenURL[it.URL], seenStory[story] = true, true
			fresh = append(fresh, it)
		}
	}
	if len(fresh) == 0 {
		return sum, nil
	}

	urls := make([]string, len(fresh))
	for i, it := range fresh {
		urls[i] = it.URL
	}
	stored, err := s.ExistingURLs(ctx, urls)
	if err != nil {
		return sum, fmt.Errorf("news ingest: check stored items: %w", err)
	}
	var todo []Item
	for _, it := range fresh {
		if stored[it.URL] {
			sum.Duplicates++
			continue
		}
		todo = append(todo, it)
	}
	if len(todo) == 0 {
		return sum, nil
	}

	if linker != nil {
		for i := range todo {
			if len(todo[i].PlayerIDs) == 0 {
				todo[i].PlayerIDs = linker.Link(todo[i].Text())
			}
		}
	}

	// Embed and save one chunk at a time. Embedding APIs rate-limit and fail
	// mid-run; saving per chunk keeps the work already paid for, and because
	// stored URLs are skipped next time, a rerun resumes where this one stopped.
	for start := 0; start < len(todo); start += ChunkSize {
		chunk := todo[start:min(start+ChunkSize, len(todo))]
		if err := embedAndSave(ctx, chunk, e, s); err != nil {
			return sum, fmt.Errorf("news ingest: items %d-%d of %d: %w", start, start+len(chunk)-1, len(todo), err)
		}
		sum.New += len(chunk)
		for _, it := range chunk {
			if len(it.PlayerIDs) > 0 {
				sum.Linked++
			}
		}
		slog.Info("saved news", "saved", sum.New, "of", len(todo))
	}
	return sum, nil
}

// storyKey identifies a story by publisher host and normalised title.
func storyKey(it Item) string {
	host := ""
	if u, err := url.Parse(it.URL); err == nil {
		host = strings.ToLower(u.Host)
	}
	return host + "|" + strings.ToLower(strings.Join(strings.Fields(it.Title), " "))
}

func embedAndSave(ctx context.Context, chunk []Item, e Embedder, s Store) error {
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
