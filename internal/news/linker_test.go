package news_test

import (
	"slices"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

func linkFixture() ([]fpl.Player, []fpl.Team) {
	teams := []fpl.Team{
		{ID: 1, Name: "Arsenal", ShortName: "ARS"},
		{ID: 2, Name: "Man City", ShortName: "MCI"},
		{ID: 3, Name: "Man Utd", ShortName: "MUN"},
		{ID: 4, Name: "Brighton", ShortName: "BHA"},
	}
	players := []fpl.Player{
		{ID: 10, WebName: "Saka", TeamID: 1, Position: fpl.MID},
		{ID: 11, WebName: "Haaland", TeamID: 2},
		{ID: 12, WebName: "João Pedro", TeamID: 4},
		{ID: 20, WebName: "B.Fernandes", TeamID: 3}, // FPL prefixes an initial when a surname is shared
		{ID: 21, WebName: "J.Fernandes", TeamID: 4},
		{ID: 30, WebName: "Rice", TeamID: 1}, // also an ordinary word
		{ID: 31, WebName: "Li", TeamID: 4},   // too short to match safely
	}
	return players, teams
}

func TestLinker(t *testing.T) {
	players, teams := linkFixture()
	l := news.NewLinker(players, teams)

	tests := []struct {
		name string
		text string
		want []int
	}{
		{"unique surname", "Saka injury 'does not look good'", []int{10}},
		{"two players in one story", "Haaland and Saka both score", []int{10, 11}},
		{"same player twice is listed once", "Saka, Saka, Saka", []int{10}},
		{"multi-word name without its accent", "Joao Pedro opens the scoring", []int{12}},
		{"accent in the text, none in the name", "Saká injury", []int{10}},
		{"all capitals with an accent", "JOÃO PEDRO opens the scoring", []int{12}},
		{"multi-word name with its accent", "João Pedro opens the scoring", []int{12}},
		{"no players", "Referees announced for the weekend", nil},
		{"lower case common word is not a player", "she cooked rice for the squad", nil},
		{"capitalised surname is", "Rice dominates midfield", []int{30}},
		{"short names never match", "Li Wei joins a club", nil},
		{"substring is not a match", "Sakamoto signs for Brighton", nil},

		// B.Fernandes and J.Fernandes share a surname, so the surname alone is
		// ambiguous and the team in the text decides.
		{"ambiguous surname without context links nobody", "Fernandes criticises coaches", nil},
		{"ambiguous surname with team picks one", "Fernandes criticises coaches after Man Utd defeat", []int{20}},
		{"ambiguous surname, other team", "Fernandes helps Brighton to victory", []int{21}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := l.Link(tc.text)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Link(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestLinker_ResultIsSorted(t *testing.T) {
	players, teams := linkFixture()
	got := news.NewLinker(players, teams).Link("Haaland then Saka then Rice")
	if !slices.IsSorted(got) || len(got) != 3 {
		t.Errorf("got %v, want 3 ids in ascending order", got)
	}
}

func TestPlayerNewsItems(t *testing.T) {
	added := time.Date(2026, 10, 21, 20, 12, 24, 123456000, time.UTC)
	players, teams := linkFixture()
	players[0].News, players[0].NewsAdded = "Hamstring injury - 75% chance of playing", &added
	players[1].News = "Suspended for one match" // news but no timestamp: unusable for ranking
	players[2].NewsAdded = &added               // timestamp but no text

	items := news.PlayerNewsItems(players, teams)

	if len(items) != 1 {
		t.Fatalf("items = %d, want only the complete one", len(items))
	}
	it := items[0]
	if it.Source != "fpl" || !slices.Equal(it.PlayerIDs, []int{10}) || !it.PublishedAt.Equal(added) {
		t.Errorf("item = %+v", it)
	}
	// Player, club, and position are in the text so the embedding knows who it is
	// about and a query like "which goalkeepers are injured" can match.
	if want := "Saka (Arsenal midfielder): Hamstring injury - 75% chance of playing"; it.Title != want {
		t.Errorf("title = %q, want %q", it.Title, want)
	}
	// A new note for the same player is a different URL, so history accumulates.
	if want := "fpl://player/10/2026-10-21T20:12:24Z"; it.URL != want {
		t.Errorf("url = %q, want %q", it.URL, want)
	}
}
