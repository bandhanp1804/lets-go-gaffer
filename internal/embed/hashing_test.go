package embed_test

import (
	"context"
	"math"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHashing_VectorsAreDeterministicUnitLengthAndRightSize(t *testing.T) {
	h := embed.NewHashing(64)
	in := []string{"Saka injury does not look good", "Haaland scores twice"}

	a, err := h.Embed(context.Background(), in, embed.Document)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := h.Embed(context.Background(), in, embed.Document)

	for i := range a {
		if len(a[i]) != 64 {
			t.Errorf("dims = %d, want 64", len(a[i]))
		}
		var norm float64
		for j := range a[i] {
			norm += float64(a[i][j]) * float64(a[i][j])
			if a[i][j] != b[i][j] {
				t.Fatalf("vector %d differs between runs", i)
			}
		}
		if math.Abs(norm-1) > 1e-5 {
			t.Errorf("squared length = %v, want 1", norm)
		}
	}
}

func TestHashing_SharedWordsMeanCloserVectors(t *testing.T) {
	h := embed.NewHashing(512)
	vecs, err := h.Embed(context.Background(), []string{
		"Saka injury doubt for Arsenal",       // 0
		"Arsenal winger Saka injury concern",  // 1: same topic
		"Manchester City sign new goalkeeper", // 2: unrelated
	}, embed.Document)
	if err != nil {
		t.Fatal(err)
	}

	near, far := cosine(vecs[0], vecs[1]), cosine(vecs[0], vecs[2])

	// This is what lets retrieval-ranking tests run offline with a real signal.
	if near <= far {
		t.Errorf("cosine(related)=%.3f should exceed cosine(unrelated)=%.3f", near, far)
	}
}

func TestHashing_IsCaseAndPunctuationInsensitive(t *testing.T) {
	h := embed.NewHashing(128)
	vecs, _ := h.Embed(context.Background(), []string{"Saka: INJURY!", "saka injury"}, embed.Document)

	if c := cosine(vecs[0], vecs[1]); c < 0.999 {
		t.Errorf("cosine = %v, want ~1 for text that differs only in case and punctuation", c)
	}
}

func TestHashing_RejectsBlankText(t *testing.T) {
	_, err := embed.NewHashing(16).Embed(context.Background(), []string{"ok", ""}, embed.Document)
	if err == nil {
		t.Error("want an error for blank text, matching the real client")
	}
}
