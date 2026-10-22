package fpl_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

const bootstrapFixture = `{
  "elements": [
    {"id": 1, "web_name": "Raya", "element_type": 1, "team": 1, "now_cost": 61, "chance_of_playing_next_round": null, "news": "", "news_added": null},
    {"id": 2, "web_name": "Saka", "element_type": 3, "team": 1, "now_cost": 100, "chance_of_playing_next_round": 75, "news": "Hamstring injury - 75% chance of playing", "news_added": "2026-10-21T20:12:24.123456Z"}
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
	// Official player news: empty and null for a fit player, populated with a timestamp otherwise.
	if raya.News != "" || raya.NewsAdded != nil {
		t.Errorf("raya news = %q added %v, want none", raya.News, raya.NewsAdded)
	}
	wantAdded := time.Date(2026, 10, 21, 20, 12, 24, 123456000, time.UTC)
	if saka.News != "Hamstring injury - 75% chance of playing" || saka.NewsAdded == nil || !saka.NewsAdded.Equal(wantAdded) {
		t.Errorf("saka news = %q added %v, want the hamstring note at %v", saka.News, saka.NewsAdded, wantAdded)
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

// entryFixture and picksFixture follow the shape of the live endpoints.
const entryFixture = `{
  "id": 4321, "name": "Gaffer FC", "player_first_name": "Pat", "player_last_name": "Doe",
  "current_event": 7, "last_deadline_bank": 1, "last_deadline_value": 1018
}`

const picksFixture = `{
  "active_chip": "wildcard",
  "automatic_subs": [],
  "entry_history": {"event": 7, "points": 69, "total_points": 463, "rank": 2550245, "overall_rank": 1757258,
                    "bank": 1, "value": 1018, "event_transfers": 2, "event_transfers_cost": 4, "points_on_bench": 6},
  "picks": [
    {"element": 82, "position": 1, "multiplier": 1, "is_captain": false, "is_vice_captain": false, "element_type": 1},
    {"element": 8, "position": 2, "multiplier": 2, "is_captain": true, "is_vice_captain": false, "element_type": 2},
    {"element": 114, "position": 3, "multiplier": 1, "is_captain": false, "is_vice_captain": true, "element_type": 2}
  ]
}`

func TestHTTPClient_FetchEntry(t *testing.T) {
	c, hit := newServer(t, "/entry/4321/", 200, entryFixture)

	e, err := c.FetchEntry(context.Background(), 4321)

	if err != nil {
		t.Fatal(err)
	}
	if len(*hit) != 1 || (*hit)[0] != "/entry/4321/" {
		t.Errorf("paths hit = %v", *hit)
	}
	want := fpl.Entry{ID: 4321, Name: "Gaffer FC", FirstName: "Pat", LastName: "Doe", CurrentEvent: 7}
	if *e != want {
		t.Errorf("entry = %+v, want %+v", *e, want)
	}
}

func TestHTTPClient_FetchPicks(t *testing.T) {
	c, hit := newServer(t, "/entry/4321/event/7/picks/", 200, picksFixture)

	p, err := c.FetchPicks(context.Background(), 4321, 7)

	if err != nil {
		t.Fatal(err)
	}
	if len(*hit) != 1 || (*hit)[0] != "/entry/4321/event/7/picks/" {
		t.Errorf("paths hit = %v", *hit)
	}
	if p.ActiveChip == nil || *p.ActiveChip != "wildcard" {
		t.Errorf("chip = %v, want wildcard", p.ActiveChip)
	}
	h := p.EntryHistory
	if h.Event != 7 || h.Points != 69 || h.TotalPoints != 463 || h.OverallRank != 1757258 ||
		h.Bank != 1 || h.Value != 1018 || h.EventTransfers != 2 || h.EventTransfersCost != 4 || h.PointsOnBench != 6 {
		t.Errorf("entry history = %+v", h)
	}
	if len(p.Picks) != 3 {
		t.Fatalf("picks = %d, want 3", len(p.Picks))
	}
	want := fpl.Pick{Element: 8, Position: 2, Multiplier: 2, IsCaptain: true, ElementType: fpl.DEF}
	if p.Picks[1] != want || !p.Picks[2].IsViceCaptain {
		t.Errorf("picks[1] = %+v (want %+v), picks[2].vice = %v", p.Picks[1], want, p.Picks[2].IsViceCaptain)
	}
}

func TestHTTPClient_NoActiveChipIsNil(t *testing.T) {
	c, _ := newServer(t, "/entry/1/event/1/picks/", 200, `{"active_chip": null, "entry_history": {}, "picks": []}`)

	p, err := c.FetchPicks(context.Background(), 1, 1)

	if err != nil || p.ActiveChip != nil {
		t.Errorf("chip = %v, err = %v; want nil chip", p.ActiveChip, err)
	}
}

func TestHTTPClient_EntryAndPicksErrors(t *testing.T) {
	c, _ := newServer(t, "/something-else/", 200, `{}`) // every real path 404s

	_, entryErr := c.FetchEntry(context.Background(), 99)
	_, picksErr := c.FetchPicks(context.Background(), 99, 8)

	for name, err := range map[string]error{"entry": entryErr, "picks": picksErr} {
		var se *httpx.StatusError
		if !errors.As(err, &se) || se.StatusCode != 404 {
			t.Errorf("%s err = %v, want *httpx.StatusError{404}", name, err)
		}
	}
	if !strings.Contains(entryErr.Error(), "99") || !strings.Contains(picksErr.Error(), "99") {
		t.Errorf("errors should name the team: %q / %q", entryErr, picksErr)
	}
}

const historyFixture2 = `{
  "current": [
    {"event": 1, "points": 53, "event_transfers": 0, "event_transfers_cost": 0, "bank": 0, "value": 1000, "points_on_bench": 3},
    {"event": 2, "points": 60, "event_transfers": 1, "event_transfers_cost": 0, "bank": 5, "value": 1001, "points_on_bench": 3}
  ],
  "past": [],
  "chips": [{"name": "wildcard", "time": "2026-09-12T16:00:00Z", "event": 5}]
}`

const transfersFixture = `[
  {"event": 7, "element_in": 8, "element_in_cost": 59, "element_out": 112, "element_out_cost": 49, "time": "2026-10-10T16:36:16.074680Z"},
  {"event": 2, "element_in": 26, "element_in_cost": 75, "element_out": 55, "element_out_cost": 80, "time": "2026-08-26T19:32:45.796569Z"}
]`

func TestHTTPClient_FetchHistory(t *testing.T) {
	c, hit := newServer(t, "/entry/4321/history/", 200, historyFixture2)

	h, err := c.FetchHistory(context.Background(), 4321)

	if err != nil {
		t.Fatal(err)
	}
	if len(*hit) != 1 || (*hit)[0] != "/entry/4321/history/" {
		t.Errorf("paths hit = %v", *hit)
	}
	if len(h.Current) != 2 || h.Current[1] != (fpl.GameweekHistory{
		Event: 2, Points: 60, EventTransfers: 1, EventTransfersCost: 0, Bank: 5, Value: 1001, PointsOnBench: 3,
	}) {
		t.Errorf("current = %+v", h.Current)
	}
	if len(h.Chips) != 1 || h.Chips[0] != (fpl.ChipUse{Name: "wildcard", Event: 5}) {
		t.Errorf("chips = %+v, want one wildcard in gameweek 5", h.Chips)
	}
}

func TestHTTPClient_FetchTransfers(t *testing.T) {
	c, hit := newServer(t, "/entry/4321/transfers/", 200, transfersFixture)

	ts, err := c.FetchTransfers(context.Background(), 4321)

	if err != nil {
		t.Fatal(err)
	}
	if len(*hit) != 1 || (*hit)[0] != "/entry/4321/transfers/" {
		t.Errorf("paths hit = %v", *hit)
	}
	if len(ts) != 2 {
		t.Fatalf("transfers = %d, want 2", len(ts))
	}
	got := ts[0]
	if got.Event != 7 || got.PlayerIn != 8 || got.PlayerInCost != 59 || got.PlayerOut != 112 || got.PlayerOutCost != 49 {
		t.Errorf("transfer = %+v", got)
	}
	if want := time.Date(2026, 10, 10, 16, 36, 16, 74680000, time.UTC); !got.Time.Equal(want) {
		t.Errorf("time = %v, want %v", got.Time, want)
	}
}

const fixturesFixture = `[
  {"id": 1, "event": 1, "team_h": 1, "team_a": 7, "team_h_difficulty": 2, "team_a_difficulty": 5, "finished": true},
  {"id": 2, "event": 8, "team_h": 7, "team_a": 3, "team_h_difficulty": 3, "team_a_difficulty": 4, "finished": false},
  {"id": 3, "event": 8, "team_h": 1, "team_a": 3, "team_h_difficulty": 2, "team_a_difficulty": 4, "finished": false},
  {"id": 4, "event": null, "team_h": 1, "team_a": 2, "team_h_difficulty": 3, "team_a_difficulty": 3, "finished": false}
]`

func TestHTTPClient_FetchFixtures(t *testing.T) {
	c, hit := newServer(t, "/fixtures/", 200, fixturesFixture)

	fs, err := c.FetchFixtures(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(*hit) != 1 || (*hit)[0] != "/fixtures/" {
		t.Errorf("paths hit = %v", *hit)
	}
	if len(fs) != 4 {
		t.Fatalf("fixtures = %d, want 4", len(fs))
	}
	want := fpl.Fixture{ID: 1, Event: 1, HomeTeam: 1, AwayTeam: 7, HomeDifficulty: 2, AwayDifficulty: 5, Finished: true}
	if fs[0] != want {
		t.Errorf("fixture 0 = %+v, want %+v", fs[0], want)
	}
	if fs[3].Event != 0 {
		t.Errorf("an unscheduled fixture (event null) has Event %d, want 0", fs[3].Event)
	}
}

func TestFixtures_DifficultiesForATeamInAGameweek(t *testing.T) {
	fs := fpl.Fixtures{
		{ID: 1, Event: 8, HomeTeam: 7, AwayTeam: 3, HomeDifficulty: 3, AwayDifficulty: 4},
		{ID: 2, Event: 8, HomeTeam: 1, AwayTeam: 3, HomeDifficulty: 2, AwayDifficulty: 5}, // team 3 plays twice: a double gameweek
		{ID: 3, Event: 9, HomeTeam: 3, AwayTeam: 1, HomeDifficulty: 3, AwayDifficulty: 3},
	}

	tests := []struct {
		name        string
		team, event int
		want        []int
	}{
		{"home side gets the home difficulty", 7, 8, []int{3}},
		{"away side gets the away difficulty", 1, 9, []int{3}},
		{"a double gameweek yields both, in fixture order", 3, 8, []int{4, 5}},
		{"a blank gameweek yields none", 7, 9, nil},
		{"an unknown team yields none", 99, 8, nil},
		{"a gameweek with no fixtures yields none", 3, 30, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fs.Difficulties(tc.team, tc.event)

			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestFixtures_OpponentsForATeamInAGameweek(t *testing.T) {
	fs := fpl.Fixtures{
		{ID: 1, Event: 8, HomeTeam: 7, AwayTeam: 3, HomeDifficulty: 3, AwayDifficulty: 4},
		{ID: 2, Event: 8, HomeTeam: 1, AwayTeam: 3, HomeDifficulty: 2, AwayDifficulty: 5}, // team 3 plays twice
		{ID: 3, Event: 9, HomeTeam: 3, AwayTeam: 1, HomeDifficulty: 3, AwayDifficulty: 3},
	}

	got := fs.Opponents(3, 8)

	want := []fpl.Opponent{
		{Team: 7, Home: false, Difficulty: 4},
		{Team: 1, Home: false, Difficulty: 5},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("away side = %+v, want %+v", got, want)
	}
	if home := fs.Opponents(7, 8); len(home) != 1 || home[0] != (fpl.Opponent{Team: 3, Home: true, Difficulty: 3}) {
		t.Errorf("home side = %+v, want the away team marked as the opponent, at home", home)
	}
	if none := fs.Opponents(7, 9); len(none) != 0 {
		t.Errorf("a blank gameweek gave %+v", none)
	}
}
