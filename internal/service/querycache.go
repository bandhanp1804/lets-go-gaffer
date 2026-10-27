package service

import (
	"strings"
	"sync"
)

// queryVectors remembers the embedding of recent questions. A question asked again (the page
// searching again after a refresh, or the same question from Ask) then costs no embedding call,
// which matters on a key limited to a few requests a minute. A vector depends only on the text
// and the model, so it never goes stale.
type queryVectors struct {
	mu    sync.Mutex
	max   int
	vecs  map[string][]float32
	order []string // oldest first, for eviction
}

const queryCacheSize = 256

// queryKey treats questions that differ only in case or spacing as the same question.
func queryKey(q string) string { return strings.ToLower(strings.Join(strings.Fields(q), " ")) }

func (c *queryVectors) get(q string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vecs[queryKey(q)]
	return v, ok
}

func (c *queryVectors) put(q string, v []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vecs == nil {
		c.vecs = map[string][]float32{}
	}
	k := queryKey(q)
	if _, ok := c.vecs[k]; ok {
		return
	}
	if limit := max(c.max, 1); len(c.order) >= limit {
		delete(c.vecs, c.order[0])
		c.order = c.order[1:]
	}
	c.vecs[k] = v
	c.order = append(c.order, k)
}
