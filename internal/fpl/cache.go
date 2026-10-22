package fpl

import (
	"context"
	"sync"
	"time"
)

// CachedClient decorates an FPLClient with a TTL cache for FetchBootstrap.
// Player history is passed through: it is fetched once per player per ingest.
type CachedClient struct {
	inner FPLClient
	ttl   time.Duration
	now   func() time.Time

	// mu is held across the upstream call on purpose: concurrent callers that
	// miss the cache queue up and share one fetch instead of stampeding the API.
	mu      sync.Mutex
	b       *Bootstrap
	fetched time.Time
}

// NewCachedClient wraps inner. now is injectable for tests; nil uses time.Now.
func NewCachedClient(inner FPLClient, ttl time.Duration, now func() time.Time) *CachedClient {
	if now == nil {
		now = time.Now
	}
	return &CachedClient{inner: inner, ttl: ttl, now: now}
}

func (c *CachedClient) FetchBootstrap(ctx context.Context) (*Bootstrap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.b != nil && c.now().Sub(c.fetched) < c.ttl {
		return c.b, nil
	}
	b, err := c.inner.FetchBootstrap(ctx)
	if err != nil {
		return nil, err // errors are never cached
	}
	c.b, c.fetched = b, c.now()
	return b, nil
}

func (c *CachedClient) FetchPlayerHistory(ctx context.Context, playerID int) ([]PlayerHistory, error) {
	return c.inner.FetchPlayerHistory(ctx, playerID)
}
