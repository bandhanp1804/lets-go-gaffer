package fpl_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl/fpltest"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newCacheFixture(ttl time.Duration) (*fpl.CachedClient, *fpltest.Fake, *fakeClock) {
	inner := fpltest.NewFake(
		&fpl.Bootstrap{Players: []fpl.Player{{ID: 1, WebName: "Raya"}}},
		map[int][]fpl.PlayerHistory{7: {{PlayerID: 7, GameweekID: 1, TotalPoints: 6}}},
	)
	clk := &fakeClock{t: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	return fpl.NewCachedClient(inner, ttl, clk.now), inner, clk
}

func TestCachedClient_BootstrapWithinTTLHitsUpstreamOnce(t *testing.T) {
	c, inner, clk := newCacheFixture(time.Minute)
	ctx := context.Background()

	first, err := c.FetchBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clk.advance(59 * time.Second)
	second, err := c.FetchBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got := inner.BootstrapCalls(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
	if first != second {
		t.Error("cached call should return the same *Bootstrap")
	}
}

func TestCachedClient_BootstrapRefetchesAfterTTL(t *testing.T) {
	c, inner, clk := newCacheFixture(time.Minute)
	ctx := context.Background()

	if _, err := c.FetchBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute + time.Second)
	if _, err := c.FetchBootstrap(ctx); err != nil {
		t.Fatal(err)
	}

	if got := inner.BootstrapCalls(); got != 2 {
		t.Errorf("upstream calls = %d, want 2", got)
	}
}

func TestCachedClient_ErrorsAreNotCached(t *testing.T) {
	c, inner, _ := newCacheFixture(time.Minute)
	ctx := context.Background()
	boom := errors.New("upstream down")

	inner.SetErr(boom)
	if _, err := c.FetchBootstrap(ctx); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}

	inner.SetErr(nil)
	b, err := c.FetchBootstrap(ctx)
	if err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if len(b.Players) != 1 {
		t.Errorf("players = %d, want 1", len(b.Players))
	}
	if got := inner.BootstrapCalls(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (the failure must not be cached)", got)
	}
}

func TestCachedClient_HistoryIsNotCached(t *testing.T) {
	c, inner, _ := newCacheFixture(time.Minute)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		h, err := c.FetchPlayerHistory(ctx, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(h) != 1 || h[0].TotalPoints != 6 {
			t.Fatalf("history = %+v, want one entry with 6 points", h)
		}
	}

	if got := inner.HistoryCalls(7); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (pass-through)", got)
	}
}
