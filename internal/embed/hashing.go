package embed

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Hashing is an offline, deterministic embedder for tests and local runs with
// no API key. It hashes each word into one of dims buckets (the "hashing
// trick"), so texts that share words get nearby vectors. It captures word
// overlap only, never meaning: do not use it for real retrieval quality.
type Hashing struct {
	dims int
}

func NewHashing(dims int) *Hashing { return &Hashing{dims: dims} }

// Embed returns one unit-length vector per text, in order. Like the real client
// it rejects blank text.
func (h *Hashing) Embed(ctx context.Context, texts []string, _ InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("embed: text %d is empty", i)
		}
		out[i] = h.vector(t)
	}
	return out, nil
}

func (h *Hashing) vector(text string) []float32 {
	v := make([]float32, h.dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		f := fnv.New64a()
		_, _ = f.Write([]byte(w))
		sum := f.Sum64()
		sign := float32(1)
		if sum&1 == 1 {
			sign = -1 // random signs make unrelated words cancel instead of pile up
		}
		v[(sum>>1)%uint64(h.dims)] += sign
	}

	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 { // no words at all (e.g. "!!!"): keep the unit-length promise
		v[0] = 1
		return v
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range v {
		v[i] *= scale
	}
	return v
}
