// Package embed turns text into vectors for semantic search.
//
// Consumers (news ingestion, retrieval) declare the small Embedder interface
// they need; Voyage and Hashing here both satisfy it.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// InputType tells Voyage whether text is a stored passage or a search query.
// The two are embedded asymmetrically, so each side must use the right one.
type InputType string

const (
	Document InputType = "document"
	Query    InputType = "query"
)

// DefaultBaseURL is Voyage AI's API host.
const DefaultBaseURL = "https://api.voyageai.com"

// VoyageConfig configures the Voyage AI embeddings client.
type VoyageConfig struct {
	APIKey     string
	Model      string // e.g. "voyage-4"
	Dimensions int    // output vector size; must match the database column
	BatchSize  int    // texts per request
	BaseURL    string // empty means DefaultBaseURL; tests point this at httptest
}

// Voyage embeds text with the Voyage AI HTTP API.
type Voyage struct {
	hc  *http.Client
	cfg VoyageConfig
}

// NewVoyage validates cfg and returns a client. Pass an *http.Client whose
// transport adds retries and rate limiting (see package httpx).
func NewVoyage(hc *http.Client, cfg VoyageConfig) (*Voyage, error) {
	switch {
	case cfg.APIKey == "":
		return nil, fmt.Errorf("voyage: API key is empty")
	case cfg.Model == "":
		return nil, fmt.Errorf("voyage: model is empty")
	case cfg.Dimensions < 1:
		return nil, fmt.Errorf("voyage: dimensions must be >= 1, got %d", cfg.Dimensions)
	case cfg.BatchSize < 1:
		return nil, fmt.Errorf("voyage: batch size must be >= 1, got %d", cfg.BatchSize)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Voyage{hc: hc, cfg: cfg}, nil
}

// Embed returns one vector per text, in input order. Large inputs are split into
// batches. Blank texts are rejected up front so no request is wasted on them.
func (v *Voyage) Embed(ctx context.Context, texts []string, kind InputType) ([][]float32, error) {
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("embed: text %d is empty", i)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += v.cfg.BatchSize {
		end := min(start+v.cfg.BatchSize, len(texts))
		vecs, err := v.embedBatch(ctx, texts[start:end], kind)
		if err != nil {
			return nil, fmt.Errorf("embed texts %d-%d: %w", start, end-1, err)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

type embedRequest struct {
	Input           []string  `json:"input"`
	Model           string    `json:"model"`
	InputType       InputType `json:"input_type"`
	OutputDimension int       `json:"output_dimension"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (v *Voyage) embedBatch(ctx context.Context, batch []string, kind InputType) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Input: batch, Model: v.cfg.Model, InputType: kind, OutputDimension: v.cfg.Dimensions})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	// bytes.NewReader lets the retry transport replay the body on a retry.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.cfg.BaseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+v.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Data) != len(batch) {
		return nil, fmt.Errorf("voyage returned %d vectors for %d texts", len(parsed.Data), len(batch))
	}

	// Place each vector by its "index" rather than trusting response order.
	out := make([][]float32, len(batch))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(batch) || out[d.Index] != nil {
			return nil, fmt.Errorf("voyage returned a bad or repeated index %d", d.Index)
		}
		if len(d.Embedding) != v.cfg.Dimensions {
			return nil, fmt.Errorf("voyage returned dimension %d, want %d", len(d.Embedding), v.cfg.Dimensions)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// statusError builds a typed error that also carries Voyage's own explanation
// ({"detail": "..."}). It never includes the request, so the key cannot leak.
func statusError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var parsed struct {
		Detail string `json:"detail"`
	}
	detail := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &parsed) == nil && parsed.Detail != "" {
		detail = parsed.Detail
	}
	return fmt.Errorf("voyage embeddings: %w: %s", &httpx.StatusError{StatusCode: resp.StatusCode}, detail)
}
