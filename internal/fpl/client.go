package fpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
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

// FetchEntry retrieves a manager's team: its name and the latest gameweek with a
// visible squad. It is public, so no login is needed.
func (c *HTTPClient) FetchEntry(ctx context.Context, teamID int) (*Entry, error) {
	var e Entry
	if err := c.get(ctx, fmt.Sprintf("/entry/%d/", teamID), &e); err != nil {
		return nil, fmt.Errorf("fetch entry %d: %w", teamID, err)
	}
	return &e, nil
}

// FetchPicks retrieves a manager's 15 players, captaincy, bank and value for a
// gameweek, as of that gameweek's deadline. A gameweek whose deadline has not
// passed is a 404; transfers made since the last deadline need a logged-in
// session and are not available here.
func (c *HTTPClient) FetchPicks(ctx context.Context, teamID, gameweek int) (*Picks, error) {
	var p Picks
	if err := c.get(ctx, fmt.Sprintf("/entry/%d/event/%d/picks/", teamID, gameweek), &p); err != nil {
		return nil, fmt.Errorf("fetch picks for team %d, gameweek %d: %w", teamID, gameweek, err)
	}
	return &p, nil
}

// FetchHistory retrieves a manager's finished gameweeks and the chips they played.
func (c *HTTPClient) FetchHistory(ctx context.Context, teamID int) (*History, error) {
	var h History
	if err := c.get(ctx, fmt.Sprintf("/entry/%d/history/", teamID), &h); err != nil {
		return nil, fmt.Errorf("fetch history for team %d: %w", teamID, err)
	}
	return &h, nil
}

// FetchTransfers retrieves every transfer a manager has made, newest first,
// with the buy and sell price of each.
func (c *HTTPClient) FetchTransfers(ctx context.Context, teamID int) ([]Transfer, error) {
	var ts []Transfer
	if err := c.get(ctx, fmt.Sprintf("/entry/%d/transfers/", teamID), &ts); err != nil {
		return nil, fmt.Errorf("fetch transfers for team %d: %w", teamID, err)
	}
	return ts, nil
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
		httpx.DrainAndClose(resp.Body) // let the connection be reused
		return fmt.Errorf("GET %s: %w", path, &httpx.StatusError{StatusCode: resp.StatusCode})
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
