package fpl_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

const bootstrapFixture = `{
  "elements": [
    {"id": 1, "web_name": "Raya", "element_type": 1, "team": 1, "now_cost": 61, "chance_of_playing_next_round": null},
    {"id": 2, "web_name": "Saka", "element_type": 3, "team": 1, "now_cost": 100, "chance_of_playing_next_round": 75}
  ],
  "teams": [{"id": 1, "name": "Arsenal", "short_name": "ARS"}],
  "events": [{"id": 1, "name": "Gameweek 1", "is_current": true}]
}`

const historyFixture = `{
  "history": [
    {"element": 7, "round": 3, "total_points": 9, "minutes": 90, "goals_scored": 1, "value": 65}
  ],
  "fixtures": []
}`

// newServer serves body at path with the given status and records requested paths.
func newServer(t *testing.T, path string, status int, body string) (*fpl.HTTPClient, *[]string) {
	t.Helper()
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.URL.Path)
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return fpl.NewHTTPClientWithBaseURL(srv.Client(), srv.URL), &hit
}

func TestHTTPClient_FetchBootstrapDecodes(t *testing.T) {
	c, hit := newServer(t, "/bootstrap-static/", 200, bootstrapFixture)

	b, err := c.FetchBootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(*hit) != 1 || (*hit)[0] != "/bootstrap-static/" {
		t.Errorf("paths hit = %v, want [/bootstrap-static/]", *hit)
	}
	if len(b.Players) != 2 || len(b.Teams) != 1 || len(b.Gameweeks) != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", len(b.Players), len(b.Teams), len(b.Gameweeks))
	}

	raya, saka := b.Players[0], b.Players[1]
	if raya.Position != fpl.GKP || raya.NowCost != 61 {
		t.Errorf("raya = %+v, want GKP at cost 61", raya)
	}
	// null must decode to nil, distinct from a real 0% chance.
	if raya.ChanceOfPlayingNextRound != nil {
		t.Errorf("raya chance = %v, want nil", *raya.ChanceOfPlayingNextRound)
	}
	if saka.ChanceOfPlayingNextRound == nil || *saka.ChanceOfPlayingNextRound != 75 {
		t.Errorf("saka chance = %v, want 75", saka.ChanceOfPlayingNextRound)
	}
	if !b.Gameweeks[0].IsCurrent {
		t.Error("gameweek 1 should be current")
	}
}

func TestHTTPClient_FetchPlayerHistoryDecodes(t *testing.T) {
	c, hit := newServer(t, "/element-summary/7/", 200, historyFixture)

	h, err := c.FetchPlayerHistory(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(*hit) != 1 || (*hit)[0] != "/element-summary/7/" {
		t.Errorf("paths hit = %v, want [/element-summary/7/]", *hit)
	}
	if len(h) != 1 {
		t.Fatalf("history len = %d, want 1", len(h))
	}
	want := fpl.PlayerHistory{PlayerID: 7, GameweekID: 3, TotalPoints: 9, Minutes: 90, GoalsScored: 1, Value: 65}
	if h[0] != want {
		t.Errorf("history[0] = %+v, want %+v", h[0], want)
	}
}

func TestHTTPClient_Errors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantMsg string
	}{
		{"malformed json", 200, `{"elements": [`, "decode"},
		{"not found", 404, `nope`, "404"},
		{"server error", 500, `oops`, "500"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newServer(t, "/bootstrap-static/", tc.status, tc.body)

			b, err := c.FetchBootstrap(context.Background())

			if err == nil {
				t.Fatal("expected an error")
			}
			if b != nil {
				t.Errorf("bootstrap = %+v, want nil on error", b)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to mention %q", err, tc.wantMsg)
			}
			if tc.status != 200 {
				var se *httpx.StatusError
				if !errors.As(err, &se) || se.StatusCode != tc.status {
					t.Errorf("err = %v, want *httpx.StatusError{%d}", err, tc.status)
				}
			}
			// CLAUDE.md: errors carry context about the operation that failed.
			if !strings.Contains(err.Error(), "fetch bootstrap") {
				t.Errorf("err = %q, want it wrapped with %q", err, "fetch bootstrap")
			}
		})
	}
}
