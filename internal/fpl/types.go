package fpl

import "time"

// Position mirrors the FPL element_type IDs.
type Position int

const (
	GKP Position = 1
	DEF Position = 2
	MID Position = 3
	FWD Position = 4
)

func (p Position) String() string {
	switch p {
	case GKP:
		return "GKP"
	case DEF:
		return "DEF"
	case MID:
		return "MID"
	case FWD:
		return "FWD"
	}
	return "UNKNOWN"
}

// Player maps the fields we care about from the bootstrap-static elements array.
// Cost is in tenths of millions (e.g. 65 = £6.5m), matching the FPL API.
type Player struct {
	ID        int      `json:"id"`
	FirstName string   `json:"first_name"`
	LastName  string   `json:"second_name"`
	WebName   string   `json:"web_name"`
	TeamID    int      `json:"team"`
	Position  Position `json:"element_type"`

	// Cost in tenths of millions (100 = £10.0m).
	NowCost int `json:"now_cost"`

	TotalPoints int    `json:"total_points"`
	Form        string `json:"form"`
	Minutes     int    `json:"minutes"`

	GoalsScored int `json:"goals_scored"`
	Assists     int `json:"assists"`
	CleanSheets int `json:"clean_sheets"`
	YellowCards int `json:"yellow_cards"`
	RedCards    int `json:"red_cards"`

	// "a"=available, "d"=doubtful, "i"=injured, "s"=suspended, "u"=unavailable
	Status string `json:"status"`

	// Null when FPL hasn't assessed the player yet.
	ChanceOfPlayingNextRound *int `json:"chance_of_playing_next_round"`

	// FPL's official note ("Hamstring injury - 75% chance of playing") and when it
	// was written. Empty and nil when the player has no news.
	News      string     `json:"news"`
	NewsAdded *time.Time `json:"news_added"`
}

// Team maps the teams array in bootstrap-static.
type Team struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
}

// Gameweek maps the events array in bootstrap-static.
type Gameweek struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	DeadlineTime string `json:"deadline_time"`
	IsCurrent    bool   `json:"is_current"`
	IsNext       bool   `json:"is_next"`
	Finished     bool   `json:"finished"`
}

// Bootstrap is the top-level response from /api/bootstrap-static/.
type Bootstrap struct {
	Players   []Player   `json:"elements"`
	Teams     []Team     `json:"teams"`
	Gameweeks []Gameweek `json:"events"`
}

// PlayerHistory is one entry from /api/element-summary/{id}/,
// representing a player's stats for a single past gameweek.
type PlayerHistory struct {
	PlayerID    int `json:"element"`
	Fixture     int `json:"fixture"`
	GameweekID  int `json:"round"`
	TotalPoints int `json:"total_points"`
	Minutes     int `json:"minutes"`
	GoalsScored int `json:"goals_scored"`
	Assists     int `json:"assists"`
	CleanSheets int `json:"clean_sheets"`
	YellowCards int `json:"yellow_cards"`
	RedCards    int `json:"red_cards"`
	// Cost at time of that gameweek (tenths of millions).
	Value int `json:"value"`
}

// elementSummaryResponse is the raw wrapper around PlayerHistory.
// Unexported because callers only need []PlayerHistory.
type elementSummaryResponse struct {
	History []PlayerHistory `json:"history"`
}

// Squad is 15 selected players with a nominated starting XI, captain, and vice-captain.
// All constraint validation happens in the optimizer package, not here.
type Squad struct {
	Players     []Player
	Starting    []int // player IDs in the starting XI (11)
	Captain     int   // player ID
	ViceCaptain int   // player ID
}
