package embed_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

const testKey = "sk-test-SECRET-key-123"

type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type"`
	OutputDimension int      `json:"output_dimension"`
}

// voyageServer is a fake Voyage API. Each returned vector has length dims and
// puts the number parsed from its text ("t17" -> 17) in element 0, so tests can
// tell which input produced which output.
type voyageServer struct {
	mu       sync.Mutex
	requests []voyageRequest
	headers  []http.Header
	paths    []string

	dims       int  // vector length to return; 0 means the requested dimension
	shuffle    bool // return data in reverse order, relying on "index"
	dropOne    bool // return one fewer vector than asked for
	failStatus int  // reply with this status and a Voyage-style error body
}

func (s *voyageServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req voyageRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.headers = append(s.headers, r.Header.Clone())
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		s.mu.Unlock()

		if s.failStatus != 0 {
			w.WriteHeader(s.failStatus)
			_, _ = w.Write([]byte(`{"detail":"Provided API key is invalid."}`))
			return
		}

		dims := s.dims
		if dims == 0 {
			dims = req.OutputDimension
		}
		type item struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var data []item
		for i, text := range req.Input {
			n, _ := strconv.Atoi(strings.TrimPrefix(text, "t"))
			v := make([]float32, dims)
			v[0] = float32(n)
			data = append(data, item{Embedding: v, Index: i})
		}
		if s.dropOne && len(data) > 0 {
			data = data[:len(data)-1]
		}
		if s.shuffle {
			for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
				data[i], data[j] = data[j], data[i]
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": req.Model, "usage": map[string]int{"total_tokens": 1}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func texts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("t%d", i)
	}
	return out
}

func newClient(t *testing.T, srv *httptest.Server, mutate func(*embed.VoyageConfig)) *embed.Voyage {
	t.Helper()
	cfg := embed.VoyageConfig{APIKey: testKey, Model: "voyage-4", Dimensions: 8, BatchSize: 64, BaseURL: srv.URL}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := embed.NewVoyage(srv.Client(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVoyage_SendsTheDocumentedRequest(t *testing.T) {
	s := &voyageServer{}
	c := newClient(t, s.start(t), nil)

	_, err := c.Embed(context.Background(), []string{"t1", "t2"}, embed.Document)

	if err != nil {
		t.Fatal(err)
	}
	if len(s.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(s.requests))
	}
	if s.paths[0] != "POST /v1/embeddings" {
		t.Errorf("called %q, want POST /v1/embeddings", s.paths[0])
	}
	if got := s.headers[0].Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want a bearer token", got)
	}
	if got := s.headers[0].Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	want := voyageRequest{Input: []string{"t1", "t2"}, Model: "voyage-4", InputType: "document", OutputDimension: 8}
	got := s.requests[0]
	if got.Model != want.Model || got.InputType != want.InputType || got.OutputDimension != want.OutputDimension ||
		strings.Join(got.Input, ",") != strings.Join(want.Input, ",") {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestVoyage_QueriesAndDocumentsAreEmbeddedDifferently(t *testing.T) {
	s := &voyageServer{}
	c := newClient(t, s.start(t), nil)

	_, _ = c.Embed(context.Background(), []string{"t1"}, embed.Query)
	_, _ = c.Embed(context.Background(), []string{"t1"}, embed.Document)

	// Voyage embeds a search query and a stored passage asymmetrically; mixing
	// them up quietly hurts retrieval quality.
	if s.requests[0].InputType != "query" || s.requests[1].InputType != "document" {
		t.Errorf("input types = %q, %q; want query, document", s.requests[0].InputType, s.requests[1].InputType)
	}
}

func TestVoyage_ReturnsVectorsInInputOrder(t *testing.T) {
	s := &voyageServer{shuffle: true} // server answers in reverse; "index" says where each belongs
	c := newClient(t, s.start(t), nil)

	vecs, err := c.Embed(context.Background(), texts(5), embed.Document)

	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vecs {
		if len(v) != 8 || int(v[0]) != i {
			t.Errorf("vecs[%d] = len %d first %v; want len 8 first %d", i, len(v), v[0], i)
		}
	}
}

func TestVoyage_SplitsLargeInputsIntoBatchesAndKeepsOrder(t *testing.T) {
	s := &voyageServer{}
	c := newClient(t, s.start(t), func(cfg *embed.VoyageConfig) { cfg.BatchSize = 64 })

	vecs, err := c.Embed(context.Background(), texts(150), embed.Document)

	if err != nil {
		t.Fatal(err)
	}
	if len(s.requests) != 3 {
		t.Fatalf("requests = %d, want 3 (64 + 64 + 22)", len(s.requests))
	}
	sizes := []int{len(s.requests[0].Input), len(s.requests[1].Input), len(s.requests[2].Input)}
	if sizes[0] != 64 || sizes[1] != 64 || sizes[2] != 22 {
		t.Errorf("batch sizes = %v, want [64 64 22]", sizes)
	}
	if len(vecs) != 150 {
		t.Fatalf("vectors = %d, want 150", len(vecs))
	}
	for i, v := range vecs {
		if int(v[0]) != i {
			t.Fatalf("vecs[%d] came from text %d: batches were reordered", i, int(v[0]))
		}
	}
}

func TestVoyage_NoInputMakesNoRequest(t *testing.T) {
	s := &voyageServer{}
	c := newClient(t, s.start(t), nil)

	vecs, err := c.Embed(context.Background(), nil, embed.Document)

	if err != nil || len(vecs) != 0 || len(s.requests) != 0 {
		t.Errorf("vecs=%v err=%v requests=%d; want nothing", vecs, err, len(s.requests))
	}
}

func TestVoyage_RejectsBlankTextsBeforeSpendingARequest(t *testing.T) {
	s := &voyageServer{}
	c := newClient(t, s.start(t), nil)

	_, err := c.Embed(context.Background(), []string{"t1", "   ", "t3"}, embed.Document)

	if err == nil || !strings.Contains(err.Error(), "text 1") {
		t.Fatalf("err = %v, want an error naming text 1", err)
	}
	if len(s.requests) != 0 {
		t.Errorf("requests = %d, want 0", len(s.requests))
	}
}

func TestVoyage_RejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		srv  *voyageServer
		want string
	}{
		{"wrong dimension", &voyageServer{dims: 5}, "dimension"},
		{"missing vector", &voyageServer{dropOne: true}, "vectors"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, tc.srv.start(t), nil)

			vecs, err := c.Embed(context.Background(), texts(3), embed.Document)

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if vecs != nil {
				t.Errorf("returned %d vectors alongside an error", len(vecs))
			}
		})
	}
}

func TestVoyage_ErrorsAreTypedExplainedAndNeverLeakTheKey(t *testing.T) {
	s := &voyageServer{failStatus: http.StatusUnauthorized}
	c := newClient(t, s.start(t), nil)

	_, err := c.Embed(context.Background(), []string{"t1"}, embed.Document)

	var se *httpx.StatusError
	if !errors.As(err, &se) || se.StatusCode != 401 {
		t.Fatalf("err = %v, want *httpx.StatusError{401}", err)
	}
	if !strings.Contains(err.Error(), "API key is invalid") {
		t.Errorf("err = %q, want Voyage's explanation included", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %q leaks the API key", err)
	}
}

func TestVoyage_ContextCancelStopsTheCall(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	c := newClient(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := c.Embed(ctx, []string{"t1"}, embed.Document)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestNewVoyage_ValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*embed.VoyageConfig)
	}{
		{"missing key", func(c *embed.VoyageConfig) { c.APIKey = "" }},
		{"missing model", func(c *embed.VoyageConfig) { c.Model = "" }},
		{"bad dimensions", func(c *embed.VoyageConfig) { c.Dimensions = 0 }},
		{"bad batch size", func(c *embed.VoyageConfig) { c.BatchSize = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := embed.VoyageConfig{APIKey: testKey, Model: "voyage-4", Dimensions: 1024, BatchSize: 64}
			tc.mutate(&cfg)

			_, err := embed.NewVoyage(http.DefaultClient, cfg)

			if err == nil {
				t.Error("want a config error")
			}
			if err != nil && strings.Contains(err.Error(), testKey) {
				t.Errorf("config error leaks the key: %v", err)
			}
		})
	}
}
