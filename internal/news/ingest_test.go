package news_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

var (
	errSource = errors.New("source down")
	errEmbed  = errors.New("embedding failed")
	errSave   = errors.New("save failed")
)

var t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func item(url, title string, ids ...int) news.Item {
	return news.Item{Source: "test", Title: title, URL: url, PublishedAt: t0, PlayerIDs: ids}
}

type fakeSource struct {
	name  string
	items func(ctx context.Context) ([]news.Item, error)
}

func (f fakeSource) Name() string                                   { return f.name }
func (f fakeSource) Items(ctx context.Context) ([]news.Item, error) { return f.items(ctx) }

func fixed(name string, items ...news.Item) fakeSource {
	return fakeSource{name, func(context.Context) ([]news.Item, error) { return items, nil }}
}

func failing(name string) fakeSource {
	return fakeSource{name, func(context.Context) ([]news.Item, error) { return nil, errSource }}
}

// fakeStore records what was asked and saved.
type fakeStore struct {
	existing    map[string]bool
	existingErr error
	saveErr     error

	saved       []news.Embedded
	saveCalls   int
	existingFor []string
}

func (s *fakeStore) ExistingURLs(_ context.Context, urls []string) (map[string]bool, error) {
	s.existingFor = urls
	if s.existingErr != nil {
		return nil, s.existingErr
	}
	out := map[string]bool{}
	for _, u := range urls {
		if s.existing[u] {
			out[u] = true
		}
	}
	return out, nil
}

func (s *fakeStore) SaveNews(_ context.Context, items []news.Embedded) error {
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, items...)
	return nil
}

func (s *fakeStore) savedURLs() []string {
	var out []string
	for _, e := range s.saved {
		out = append(out, e.URL)
	}
	return out
}

// spyEmbedder wraps the offline embedder and records every text it is asked for.
type spyEmbedder struct {
	inner  embed.Hashing
	err    error
	short  bool // return one vector too few
	failAt int  // fail the Nth Embed call (1-based); 0 means never

	mu    sync.Mutex
	texts [][]string
	kinds []embed.InputType
}

func newSpy() *spyEmbedder { return &spyEmbedder{inner: *embed.NewHashing(16)} }

func (e *spyEmbedder) Embed(ctx context.Context, texts []string, kind embed.InputType) ([][]float32, error) {
	e.mu.Lock()
	e.texts = append(e.texts, slices.Clone(texts))
	e.kinds = append(e.kinds, kind)
	calls := len(e.texts)
	e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	if e.failAt > 0 && calls == e.failAt {
		return nil, errEmbed
	}
	vecs, err := e.inner.Embed(ctx, texts, kind)
	if e.short && len(vecs) > 0 {
		vecs = vecs[:len(vecs)-1]
	}
	return vecs, err
}

func (e *spyEmbedder) embeddedTexts() []string {
	var all []string
	for _, b := range e.texts {
		all = append(all, b...)
	}
	return all
}

func linker() *news.Linker { return news.NewLinker(linkFixture()) }

func TestIngest_EmbedsLinksAndSavesNewItems(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()
	sources := []news.Source{
		fixed("rss", item("https://a/1", "Saka injury doubt for Arsenal")),
		fixed("fpl", item("fpl://player/11/x", "Haaland (Man City): Knock", 11)), // already linked by its source
	}

	sum, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	if len(st.saved) != 2 {
		t.Fatalf("saved %d items, want 2", len(st.saved))
	}
	if !slices.Equal(st.saved[0].PlayerIDs, []int{10}) {
		t.Errorf("rss item players = %v, want [10] found by the linker", st.saved[0].PlayerIDs)
	}
	if !slices.Equal(st.saved[1].PlayerIDs, []int{11}) {
		t.Errorf("fpl item players = %v, want its own [11] kept", st.saved[1].PlayerIDs)
	}
	for _, e := range st.saved {
		if len(e.Embedding) != 16 {
			t.Errorf("embedding for %s has %d dims, want 16", e.URL, len(e.Embedding))
		}
	}
	if sum.Fetched != 2 || sum.New != 2 || sum.Duplicates != 0 || sum.Linked != 2 {
		t.Errorf("summary = %+v, want Fetched 2, New 2, Duplicates 0, Linked 2", sum)
	}
	// Stored passages are embedded as documents.
	if len(emb.kinds) != 1 || emb.kinds[0] != embed.Document {
		t.Errorf("embedding kinds = %v, want one Document call", emb.kinds)
	}
	if !slices.Equal(emb.embeddedTexts(), []string{"Saka injury doubt for Arsenal", "Haaland (Man City): Knock"}) {
		t.Errorf("embedded texts = %q", emb.embeddedTexts())
	}
}

func TestIngest_NeverReEmbedsStoredItems(t *testing.T) {
	st := &fakeStore{existing: map[string]bool{"https://a/old": true}}
	emb := newSpy()
	sources := []news.Source{fixed("rss", item("https://a/old", "Old story"), item("https://a/new", "New story"))}

	sum, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	// Embeddings cost tokens and time; a story we already hold must be skipped.
	if !slices.Equal(emb.embeddedTexts(), []string{"New story"}) {
		t.Errorf("embedded %q, want only the new story", emb.embeddedTexts())
	}
	if !slices.Equal(st.savedURLs(), []string{"https://a/new"}) {
		t.Errorf("saved %v, want only the new story", st.savedURLs())
	}
	if sum.Fetched != 2 || sum.New != 1 || sum.Duplicates != 1 {
		t.Errorf("summary = %+v, want Fetched 2, New 1, Duplicates 1", sum)
	}
}

func TestIngest_NothingNewSpendsNothing(t *testing.T) {
	st := &fakeStore{existing: map[string]bool{"https://a/1": true}}
	emb := newSpy()

	sum, err := news.Ingest(context.Background(), []news.Source{fixed("rss", item("https://a/1", "Story"))}, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	if len(emb.texts) != 0 || st.saveCalls != 0 {
		t.Errorf("embed calls = %d, save calls = %d; want none", len(emb.texts), st.saveCalls)
	}
	if sum.New != 0 || sum.Duplicates != 1 {
		t.Errorf("summary = %+v", sum)
	}
}

func TestIngest_SameURLFromTwoSourcesIsSavedOnce(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()
	sources := []news.Source{
		fixed("a", item("https://x/same", "Story one")),
		fixed("b", item("https://x/same", "Story one (copy)"), item("https://x/other", "Other")),
	}

	sum, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.savedURLs(), []string{"https://x/same", "https://x/other"}) {
		t.Errorf("saved %v, want each URL once, first source winning", st.savedURLs())
	}
	if sum.Fetched != 3 || sum.New != 2 || sum.Duplicates != 1 {
		t.Errorf("summary = %+v", sum)
	}
}

func TestIngest_SamePublisherSameTitleIsOneStoryEvenWithDifferentURLs(t *testing.T) {
	// Sky files one article under two feed sections: the URLs differ only in a
	// section number, so URL identity alone cannot see they are the same story.
	st, emb := &fakeStore{}, newSpy()
	sources := []news.Source{
		fixed("sky-news", item("https://www.skysports.com/football/news/12040/777/hurzeler", "Papers: Hurzeler to replace Carrick")),
		fixed("sky-football", item("https://www.skysports.com/football/news/11095/777/hurzeler", "  Papers: Hurzeler  to replace CARRICK ")),
	}

	sum, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.savedURLs(), []string{"https://www.skysports.com/football/news/12040/777/hurzeler"}) {
		t.Errorf("saved %v, want only the first feed's copy", st.savedURLs())
	}
	if sum.Fetched != 2 || sum.New != 1 || sum.Duplicates != 1 {
		t.Errorf("summary = %+v, want Fetched 2, New 1, Duplicates 1", sum)
	}
}

func TestIngest_SameTitleFromDifferentPublishersAreDifferentStories(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()
	sources := []news.Source{
		fixed("bbc", item("https://www.bbc.co.uk/sport/a", "Transfer latest")),
		fixed("sky", item("https://www.skysports.com/b", "Transfer latest")),
	}

	_, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil || len(st.saved) != 2 {
		t.Errorf("err=%v saved=%d; want both: a shared headline across publishers is a coincidence", err, len(st.saved))
	}
}

func TestIngest_OneFailingSourceDoesNotStopTheOthers(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()
	sources := []news.Source{failing("sky"), fixed("bbc", item("https://b/1", "BBC story"))}

	sum, err := news.Ingest(context.Background(), sources, linker(), emb, st)

	if err != nil {
		t.Fatalf("err = %v, want nil: one of two sources worked", err)
	}
	if !slices.Equal(st.savedURLs(), []string{"https://b/1"}) {
		t.Errorf("saved %v, want the BBC story", st.savedURLs())
	}
	if !errors.Is(sum.SourceErrors["sky"], errSource) || len(sum.SourceErrors) != 1 {
		t.Errorf("SourceErrors = %v, want sky -> errSource", sum.SourceErrors)
	}
}

func TestIngest_AllSourcesFailingIsAnError(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()

	sum, err := news.Ingest(context.Background(), []news.Source{failing("a"), failing("b")}, linker(), emb, st)

	if !errors.Is(err, errSource) {
		t.Fatalf("err = %v, want it to wrap the source error", err)
	}
	if len(sum.SourceErrors) != 2 || len(emb.texts) != 0 {
		t.Errorf("SourceErrors = %v, embed calls = %d", sum.SourceErrors, len(emb.texts))
	}
}

func TestIngest_ResultsFollowSourceOrderNotFinishOrder(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()
	slow := fakeSource{"slow", func(context.Context) ([]news.Item, error) {
		time.Sleep(30 * time.Millisecond)
		return []news.Item{item("https://s/1", "slow one"), item("https://s/2", "slow two")}, nil
	}}

	_, err := news.Ingest(context.Background(), []news.Source{slow, fixed("fast", item("https://f/1", "fast one"))}, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	// Concurrent fetching must not make the output order depend on timing.
	if want := []string{"https://s/1", "https://s/2", "https://f/1"}; !slices.Equal(st.savedURLs(), want) {
		t.Errorf("saved %v, want %v", st.savedURLs(), want)
	}
}

func TestIngest_FetchesSourcesConcurrently(t *testing.T) {
	const n = 3
	var arrived atomic.Int32
	release := make(chan struct{})
	mk := func(name string) fakeSource {
		return fakeSource{name, func(context.Context) ([]news.Item, error) {
			if arrived.Add(1) == n {
				close(release)
			}
			select {
			case <-release:
				return nil, nil
			case <-time.After(2 * time.Second):
				return nil, errors.New("sources are not running in parallel")
			}
		}}
	}

	sum, err := news.Ingest(context.Background(), []news.Source{mk("a"), mk("b"), mk("c")}, linker(), newSpy(), &fakeStore{})

	// Each source waits for the others to start; a sequential loop would time out.
	if err != nil || len(sum.SourceErrors) != 0 {
		t.Fatalf("err = %v, source errors = %v", err, sum.SourceErrors)
	}
}

func TestIngest_PipelineFailuresSaveNothing(t *testing.T) {
	good := []news.Source{fixed("rss", item("https://a/1", "Story"))}
	tests := []struct {
		name    string
		store   *fakeStore
		emb     *spyEmbedder
		wantErr error
		wantMsg string
	}{
		{"lookup fails", &fakeStore{existingErr: errSave}, newSpy(), errSave, ""},
		{"embedding fails", &fakeStore{}, &spyEmbedder{inner: *embed.NewHashing(16), err: errEmbed}, errEmbed, ""},
		{"embedder returns too few vectors", &fakeStore{}, &spyEmbedder{inner: *embed.NewHashing(16), short: true}, nil, "vectors"},
		{"save fails", &fakeStore{saveErr: errSave}, newSpy(), errSave, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := news.Ingest(context.Background(), good, linker(), tc.emb, tc.store)

			if err == nil {
				t.Fatal("want an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to mention %q", err, tc.wantMsg)
			}
			if len(tc.store.saved) != 0 {
				t.Errorf("saved %d items despite the failure", len(tc.store.saved))
			}
		})
	}
}

func TestIngest_ContextCancelStopsFetching(t *testing.T) {
	block := fakeSource{"slow", func(ctx context.Context) ([]news.Item, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := news.Ingest(ctx, []news.Source{block}, linker(), newSpy(), &fakeStore{})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Error("took too long to stop")
	}
}

func manyItems(n int) []news.Item {
	items := make([]news.Item, n)
	for i := range items {
		items[i] = item(fmt.Sprintf("https://a/%03d", i), fmt.Sprintf("Story number %d", i))
	}
	return items
}

func TestIngest_EmbedsAndSavesChunkByChunk(t *testing.T) {
	st, emb := &fakeStore{}, newSpy()

	sum, err := news.Ingest(context.Background(), []news.Source{fixed("rss", manyItems(130)...)}, linker(), emb, st)

	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, b := range emb.texts {
		sizes = append(sizes, len(b))
	}
	if !slices.Equal(sizes, []int{news.ChunkSize, news.ChunkSize, 130 - 2*news.ChunkSize}) {
		t.Errorf("embed call sizes = %v, want chunks of %d", sizes, news.ChunkSize)
	}
	// One save per chunk is what makes progress durable.
	if st.saveCalls != 3 || len(st.saved) != 130 || sum.New != 130 {
		t.Errorf("save calls=%d saved=%d New=%d; want 3, 130, 130", st.saveCalls, len(st.saved), sum.New)
	}
}

func TestIngest_AFailureMidwayKeepsEarlierChunksAndTheNextRunResumes(t *testing.T) {
	// Embedding APIs rate-limit and fail. The work already paid for must survive.
	st := &fakeStore{}
	sources := []news.Source{fixed("rss", manyItems(150)...)}

	sum, err := news.Ingest(context.Background(), sources, linker(), &spyEmbedder{inner: *embed.NewHashing(16), failAt: 2}, st)

	if !errors.Is(err, errEmbed) {
		t.Fatalf("err = %v, want it to wrap errEmbed", err)
	}
	if len(st.saved) != news.ChunkSize || sum.New != news.ChunkSize {
		t.Fatalf("saved %d, New %d; want the first chunk (%d) kept", len(st.saved), sum.New, news.ChunkSize)
	}

	// Second run: the store now knows the first chunk, so only the rest is embedded.
	st.existing = map[string]bool{}
	for _, u := range st.savedURLs() {
		st.existing[u] = true
	}
	healthy := newSpy()
	sum2, err := news.Ingest(context.Background(), sources, linker(), healthy, st)

	if err != nil {
		t.Fatal(err)
	}
	if got := len(healthy.embeddedTexts()); got != 150-news.ChunkSize {
		t.Errorf("second run embedded %d texts, want only the remaining %d", got, 150-news.ChunkSize)
	}
	if len(st.saved) != 150 || sum2.New != 150-news.ChunkSize || sum2.Duplicates != news.ChunkSize {
		t.Errorf("saved=%d New=%d Duplicates=%d; want 150 total, %d new, %d already stored",
			len(st.saved), sum2.New, sum2.Duplicates, 150-news.ChunkSize, news.ChunkSize)
	}
}

func TestIngest_NilLinkerStillWorks(t *testing.T) {
	st := &fakeStore{}

	_, err := news.Ingest(context.Background(), []news.Source{fixed("rss", item("https://a/1", "Saka injury"))}, nil, newSpy(), st)

	if err != nil || len(st.saved) != 1 || len(st.saved[0].PlayerIDs) != 0 {
		t.Errorf("err=%v saved=%d; want one unlinked item", err, len(st.saved))
	}
}
