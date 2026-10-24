package workflows

import (
	"context"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

const (
	relinkPage  = 200
	reembedPage = news.ChunkSize
	// pagesPerRun bounds one run's history; the backfill then continues as a new run from its cursor.
	pagesPerRun = 100
)

// BackfillInput is a backfill's cursor and running totals, carried across continue-as-new.
type BackfillInput struct {
	After   int64 // the last chunk id done
	Pages   int
	Changed int
}

// BackfillResult is what a backfill did in all.
type BackfillResult struct {
	Pages   int `json:"pages"`
	Changed int `json:"changed"`
}

// PageResult is one page of a backfill.
type PageResult struct {
	Next    int64
	Changed int
	Done    bool
}

// Relink works out again which players every stored story mentions, a page at a time. The
// cursor is workflow state, so a worker that dies mid-way resumes at the next page.
func Relink(ctx workflow.Context, in BackfillInput) (BackfillResult, error) {
	var a *Activities
	return backfill(ctx, in, a.RelinkPage, Relink)
}

// Reembed embeds every stored chunk again with the configured model, for after the embedding
// model changes. It pages the same way as Relink.
func Reembed(ctx workflow.Context, in BackfillInput) (BackfillResult, error) {
	var a *Activities
	return backfill(ctx, in, a.ReembedPage, Reembed)
}

func backfill(ctx workflow.Context, in BackfillInput, page, self any) (BackfillResult, error) {
	pageCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second, BackoffCoefficient: 2, MaximumInterval: 2 * time.Minute, MaximumAttempts: 8,
		},
	})
	for run := 0; ; run++ {
		if run == pagesPerRun {
			return BackfillResult{}, workflow.NewContinueAsNewError(ctx, self, in)
		}
		var p PageResult
		if err := workflow.ExecuteActivity(pageCtx, page, in.After).Get(ctx, &p); err != nil {
			return BackfillResult{Pages: in.Pages, Changed: in.Changed}, err
		}
		in.After, in.Pages, in.Changed = p.Next, in.Pages+1, in.Changed+p.Changed
		if p.Done {
			return BackfillResult{Pages: in.Pages, Changed: in.Changed}, nil
		}
	}
}

// RelinkPage relinks one page of stories after the cursor.
func (a *Activities) RelinkPage(ctx context.Context, after int64) (PageResult, error) {
	players, err := a.Store.ListPlayers(ctx)
	if err != nil {
		return PageResult{}, err
	}
	teams, err := a.Store.ListTeams(ctx)
	if err != nil {
		return PageResult{}, err
	}
	p, err := a.Store.RelinkNewsPage(ctx, after, relinkPage, news.NewLinker(players, teams).Link)
	return PageResult(p), err
}

// ReembedPage embeds one page of chunks after the cursor again and replaces their vectors.
func (a *Activities) ReembedPage(ctx context.Context, after int64) (PageResult, error) {
	if a.Embedder == nil {
		return PageResult{}, permanent("VOYAGE_API_KEY is not set, so nothing can be embedded")
	}
	chunks, err := a.Store.ChunksAfter(ctx, after, reembedPage)
	if err != nil {
		return PageResult{}, err
	}
	if len(chunks) == 0 {
		return PageResult{Next: after, Done: true}, nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := a.Embedder.Embed(ctx, texts, embed.Document)
	if err != nil {
		return PageResult{}, err
	}
	update := make(map[int64][]float32, len(chunks))
	for i, c := range chunks {
		update[c.ID] = vecs[i]
	}
	if err := a.Store.UpdateEmbeddings(ctx, update); err != nil {
		return PageResult{}, err
	}
	return PageResult{Next: chunks[len(chunks)-1].ID, Changed: len(chunks), Done: len(chunks) < reembedPage}, nil
}
