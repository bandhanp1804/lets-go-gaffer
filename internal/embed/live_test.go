package embed_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// Live test against the real Voyage API. It skips unless VOYAGE_API_KEY is set,
// so CI and plain `go test ./...` never spend tokens. It sends a few short strings.
func TestVoyage_Live(t *testing.T) {
	key := os.Getenv("VOYAGE_API_KEY")
	if key == "" {
		t.Skip("VOYAGE_API_KEY not set")
	}
	hc := &http.Client{Transport: httpx.NewRetryTransport(nil, httpx.RetryConfig{
		MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 10 * time.Second,
	})}
	c, err := embed.NewVoyage(hc, embed.VoyageConfig{APIKey: key, Model: "voyage-4", Dimensions: 1024, BatchSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	docs, err := c.Embed(ctx, []string{
		"Arsenal winger Bukayo Saka is a doubt with a hamstring injury",
		"Manchester City have agreed a deal for a new goalkeeper",
	}, embed.Document)
	if err != nil {
		t.Fatal(err)
	}
	query, err := c.Embed(ctx, []string{"is Saka injured?"}, embed.Query)
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 2 || len(docs[0]) != 1024 || len(query[0]) != 1024 {
		t.Fatalf("got %d docs with dims %d; want 2 docs of 1024", len(docs), len(docs[0]))
	}
	related, unrelated := cosine(query[0], docs[0]), cosine(query[0], docs[1])
	t.Logf("cosine: related=%.3f unrelated=%.3f", related, unrelated)
	if related <= unrelated {
		t.Errorf("the real model ranked the unrelated article higher (%.3f <= %.3f)", related, unrelated)
	}
}
