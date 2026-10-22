package fpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const baseURL = "https://fantasy.premierleague.com/api"

// FPLClient is the interface every caller depends on.
// Defined here so tests can inject a fake without importing net/http.
type FPLClient interface {
	FetchBootstrap(ctx context.Context) (*Bootstrap, error)
	FetchPlayerHistory(ctx context.Context, playerID int) ([]PlayerHistory, error)
}

// HTTPClient implements FPLClient against the real FPL API.
type HTTPClient struct {
	http    *http.Client
	baseURL string
}

// NewHTTPClient returns an FPLClient backed by a real HTTP connection.
// Pass a custom *http.Client to control timeouts, transport, etc.
func NewHTTPClient(hc *http.Client) *HTTPClient {
	return NewHTTPClientWithBaseURL(hc, baseURL)
}

// NewHTTPClientWithBaseURL is NewHTTPClient against a different host (tests).
func NewHTTPClientWithBaseURL(hc *http.Client, url string) *HTTPClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &HTTPClient{http: hc, baseURL: url}
}

// FetchBootstrap retrieves the full season snapshot: all players, teams, and gameweeks.
func (c *HTTPClient) FetchBootstrap(ctx context.Context) (*Bootstrap, error) {
	var b Bootstrap
	if err := c.get(ctx, "/bootstrap-static/", &b); err != nil {
		return nil, fmt.Errorf("fetch bootstrap: %w", err)
	}
	return &b, nil
}

// FetchPlayerHistory retrieves per-gameweek history for a single player.
func (c *HTTPClient) FetchPlayerHistory(ctx context.Context, playerID int) ([]PlayerHistory, error) {
	var resp elementSummaryResponse
	path := fmt.Sprintf("/element-summary/%d/", playerID)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("fetch player history %d: %w", playerID, err)
	}
	return resp.History, nil
}

// get performs a GET request and JSON-decodes the response into dst.
func (c *HTTPClient) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		drainAndClose(resp.Body) // let the connection be reused
		return fmt.Errorf("GET %s: %w", path, &StatusError{StatusCode: resp.StatusCode})
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
