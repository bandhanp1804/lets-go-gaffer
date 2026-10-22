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

// Entry is a manager's team as returned by /api/entry/{id}/.
type Entry struct {
	ID           int    `json:"id"`
	Name         string `json:"name"` // the team name
	FirstName    string `json:"player_first_name"`
	LastName     string `json:"player_last_name"`
	CurrentEvent int    `json:"current_event"` // latest gameweek with a visible squad; 0 before the first
}

// Pick is one of a manager's 15 players for a gameweek. Position 1-11 is the
// starting XI in order, 12-15 the bench; Multiplier is 2 for the captain.
type Pick struct {
	Element       int      `json:"element"` // player id
	Position      int      `json:"position"`
	Multiplier    int      `json:"multiplier"`
	IsCaptain     bool     `json:"is_captain"`
	IsViceCaptain bool     `json:"is_vice_captain"`
	ElementType   Position `json:"element_type"`
}

// EntryHistory summarises a manager's gameweek. Bank and Value are in tenths of £m.
type EntryHistory struct {
	Event              int `json:"event"`
	Points             int `json:"points"`
	TotalPoints        int `json:"total_points"`
	OverallRank        int `json:"overall_rank"`
	Bank               int `json:"bank"`
	Value              int `json:"value"`
	EventTransfers     int `json:"event_transfers"`
	EventTransfersCost int `json:"event_transfers_cost"`
	PointsOnBench      int `json:"points_on_bench"`
}

// Picks is a manager's squad for one gameweek, as of that gameweek's deadline,
// from /api/entry/{id}/event/{gw}/picks/.
type Picks struct {
	ActiveChip   *string      `json:"active_chip"` // nil when no chip was played
	EntryHistory EntryHistory `json:"entry_history"`
	Picks        []Pick       `json:"picks"`
}

// GameweekHistory is one finished gameweek of a manager's season. Bank and Value
// are in tenths of £m; Value is the squad's market value at that deadline, not
// the sum of its selling prices.
type GameweekHistory struct {
	Event              int `json:"event"`
	Points             int `json:"points"`
	EventTransfers     int `json:"event_transfers"`
	EventTransfersCost int `json:"event_transfers_cost"` // points lost to transfers beyond the free ones
	Bank               int `json:"bank"`
	Value              int `json:"value"`
	PointsOnBench      int `json:"points_on_bench"`
}

// ChipUse records a chip (wildcard, freehit, bboost, 3xc) played in a gameweek.
type ChipUse struct {
	Name  string `json:"name"`
	Event int    `json:"event"`
}

// History is a manager's season so far, from /api/entry/{id}/history/.
type History struct {
	Current []GameweekHistory `json:"current"`
	Chips   []ChipUse         `json:"chips"`
}

// Transfer is one player swap a manager made. The costs are the prices actually
// paid and received, in tenths of £m: PlayerOutCost is the selling price.
type Transfer struct {
	Event         int       `json:"event"`
	PlayerIn      int       `json:"element_in"`
	PlayerInCost  int       `json:"element_in_cost"`
	PlayerOut     int       `json:"element_out"`
	PlayerOutCost int       `json:"element_out_cost"`
	Time          time.Time `json:"time"`
}

// Fixture is one scheduled or played match. Difficulty is FPL's rating of how
// hard the match is for that side, 1 (easiest) to 5. Event is the gameweek, or 0
// while the match is unscheduled.
type Fixture struct {
	ID             int  `json:"id"`
	Event          int  `json:"event"`
	HomeTeam       int  `json:"team_h"`
	AwayTeam       int  `json:"team_a"`
	HomeDifficulty int  `json:"team_h_difficulty"`
	AwayDifficulty int  `json:"team_a_difficulty"`
	Finished       bool `json:"finished"`
}

// Fixtures is a season's schedule.
type Fixtures []Fixture

// Difficulties returns the difficulty of each match a team plays in a gameweek,
// in fixture order. A blank gameweek gives none; a double gameweek gives two.
func (fs Fixtures) Difficulties(team, event int) []int {
	var out []int
	for _, f := range fs {
		if f.Event != event {
			continue
		}
		switch team {
		case f.HomeTeam:
			out = append(out, f.HomeDifficulty)
		case f.AwayTeam:
			out = append(out, f.AwayDifficulty)
		}
	}
	return out
}
