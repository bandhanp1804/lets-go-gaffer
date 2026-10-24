package service

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// The views below are what the assistant (and later the HTTP API) sees. Every
// number is already computed and every price already formatted ("£6.1m"), so no
// consumer has to do arithmetic or convert tenths of millions: the model narrates
// results, it does not calculate them.

// PlayerView describes one player.
type PlayerView struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Team        string  `json:"team"`
	Position    string  `json:"position"`
	Price       string  `json:"price"`
	SellPrice   string  `json:"sell_price,omitempty"` // what an owned player would sell for
	XP          float64 `json:"xp"`                   // expected points per gameweek, as stated by the view that returns it
	Form        string  `json:"form"`
	TotalPoints int     `json:"total_points"`
	Status      string  `json:"status"` // "available", "doubtful (50%)", "injured", ...
	News        string  `json:"news,omitempty"`
	Fixture     string  `json:"fixture,omitempty"`   // next gameweek, e.g. "ARS (H)"
	GWPoints    *int    `json:"gw_points,omitempty"` // points in the gameweek being scored, on My team only
}

// MyTeamView is the manager's squad.
type MyTeamView struct {
	Manager        string       `json:"manager"`
	TeamName       string       `json:"team_name"`
	Gameweek       int          `json:"gameweek"`    // the gameweek being picked for
	Source         string       `json:"source"`      // "live" or "public"
	AsOf           string       `json:"squad_as_of"` // when the squad is from, to be quoted as written
	Starting       []PlayerView `json:"starting"`
	Bench          []PlayerView `json:"bench"`
	Captain        string       `json:"captain"`
	ViceCaptain    string       `json:"vice_captain"`
	Bank           string       `json:"bank"`
	FreeTransfers  int          `json:"free_transfers_left"`
	TransfersMade  int          `json:"transfers_made_this_week"`
	HitSoFar       int          `json:"points_hit_so_far"`
	ChipsAvailable []string     `json:"chips_available,omitempty"`
	Notes          []string     `json:"notes"`    // read these: they say how fresh the data is
	XPBasis        string       `json:"xp_basis"` // what each player's "xp" is: one gameweek's forecast
	// Points is how the team is scoring in the gameweek being played (or the last one finished),
	// from public data. Nil when FPL's points could not be read or the season has not started.
	Points *PointsView `json:"points"`
}

// CloseCallView says how nearly a benched player started.
type CloseCallView struct {
	Bench     string  `json:"bench_player"`
	Displaces string  `json:"would_replace"`
	Margin    float64 `json:"expected_points_behind"`
}

// CaptainOptionView is one starter as a captain: what his double is worth and what choosing him costs.
type CaptainOptionView struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Team      string  `json:"team"`
	XP        float64 `json:"xp"`
	AsCaptain float64 `json:"as_captain"` // his expected points counted twice
	Cost      float64 `json:"cost"`       // expected points given up against the best captain; 0 for the best
	Captain   bool    `json:"is_captain"`
	Vice      bool    `json:"is_vice_captain"`
}

// FormationView is the cost of choosing a different formation.
type FormationView struct {
	Formation string  `json:"formation"`
	Delta     float64 `json:"expected_points_vs_best"` // zero or negative
}

// LineupView is the recommended XI for the upcoming gameweek.
type LineupView struct {
	Gameweek           int                 `json:"gameweek"`
	Source             string              `json:"source"`
	AsOf               string              `json:"squad_as_of"`
	Formation          string              `json:"formation"`
	ExpectedPoints     float64             `json:"expected_points"` // the XI, captain counted twice
	Starting           []PlayerView        `json:"starting"`
	Bench              []PlayerView        `json:"bench"` // spare goalkeeper first, then in auto-sub order
	Captain            string              `json:"captain"`
	ViceCaptain        string              `json:"vice_captain"`
	CaptainLead        float64             `json:"captain_lead_over_vice"` // under 0.3 is a toss-up
	CaptainID          int                 `json:"captain_id"`
	ViceCaptainID      int                 `json:"vice_captain_id"`
	CaptainChosen      bool                `json:"captain_chosen"`      // the manager picked the armband instead of taking the recommendation
	RecommendedCaptain string              `json:"recommended_captain"` // the best captain for this XI
	CaptainCost        float64             `json:"captain_cost"`        // expected points the chosen captain gives up against the recommended one; 0 when they are the same
	CaptainOptions     []CaptainOptionView `json:"captain_options"`
	CloseCalls         []CloseCallView     `json:"close_calls"`
	OtherFormations    []FormationView     `json:"other_formations"`
	XPBasis            string              `json:"xp_basis"`                // what each "xp" is
	BestFormation      string              `json:"best_formation"`          // the best of all, whichever is shown
	VsBest             float64             `json:"expected_points_vs_best"` // the shown formation against the best: zero or negative
	AllFormations      []FormationView     `json:"all_formations"`          // every legal formation, best first, with the shown one included
	CurrentLineupIs    string              `json:"compared_with"`           // what the squad's current XI is
	SameAsCurrent      bool                `json:"same_xi_as_current"`
	PlayersIn          []string            `json:"players_in,omitempty"`
	PlayersOut         []string            `json:"players_out,omitempty"`
	Notes              []string            `json:"notes"`
}

// MoveView is one swap.
type MoveView struct {
	Summary string     `json:"summary"` // the whole move in one sentence, to be quoted rather than reassembled
	Out     PlayerView `json:"out"`
	In      PlayerView `json:"in"`
}

// MoveSummary states a swap in one sentence. A swap involves two prices (what the
// outgoing player sells for, what the incoming one costs) and two fixtures, and a
// model handed the raw fields tends to cross them, so Go writes the sentence.
func MoveSummary(out, in PlayerView) string {
	side := func(p PlayerView, price string) string {
		s := fmt.Sprintf("%s (%s, %s)", p.Name, p.Team, p.Position)
		if price != "" {
			s += ", " + price
		}
		return s
	}
	fixture := func(p PlayerView) string {
		if p.Fixture == "" {
			return ""
		}
		return fmt.Sprintf(", %s plays %s", p.Name, p.Fixture)
	}
	return fmt.Sprintf("%s out, %s in%s%s",
		side(out, "sells for "+out.SellPrice), side(in, "costs "+in.Price), fixture(out), fixture(in))
}

// SuggestionView is a set of moves and what they are worth.
type SuggestionView struct {
	Moves           []MoveView `json:"moves"`
	Transfers       int        `json:"transfers"`
	Hit             int        `json:"points_hit"`
	GainPerGameweek float64    `json:"expected_gain_per_gameweek"`
	NetPoints       float64    `json:"net_points_over_horizon"`    // gain over the horizon minus the hit
	NetVsFirst      float64    `json:"net_points_vs_first_option"` // this option's net minus the first listed option's; zero for the first. Under 0.3 either way is a toss-up
	BankAfter       string     `json:"bank_after"`
}

// TransfersView is the transfer advice.
type TransfersView struct {
	Gameweek         int              `json:"gameweek"`
	Source           string           `json:"source"`
	AsOf             string           `json:"squad_as_of"`
	Wildcard         bool             `json:"wildcard"`
	FreeTransfers    int              `json:"free_transfers_left"`
	Bank             string           `json:"bank"`
	Horizon          int              `json:"horizon_gameweeks"`
	PendingTransfers int              `json:"transfers_already_made"`
	PendingHit       int              `json:"points_hit_so_far"`
	XPBasis          string           `json:"xp_basis"`    // what each player's "xp" is: an average over the horizon
	Suggestions      []SuggestionView `json:"suggestions"` // empty means: keep the squad as it is
	Notes            []string         `json:"notes"`
}

// NewsView is one retrieved story.
type NewsView struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Source     string   `json:"source"`
	URL        string   `json:"url"`
	Published  string   `json:"published"`
	Age        string   `json:"age"`
	Players    []string `json:"players,omitempty"`
	Similarity float64  `json:"similarity"`
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// formatMoney renders tenths of £m: 61 -> "£6.1m".
func formatMoney(tenths int) string { return fmt.Sprintf("£%d.%dm", tenths/10, tenths%10) }

// age renders how long ago t was, coarsely: "5m", "3h", "2d".
func age(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func ago(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

func statusText(p fpl.Player) string {
	label := map[string]string{"a": "available", "d": "doubtful", "i": "injured", "s": "suspended", "u": "unavailable"}[p.Status]
	if label == "" {
		label = p.Status
	}
	if c := p.ChanceOfPlayingNextRound; c != nil && *c < 100 && p.Status != "i" && p.Status != "s" && p.Status != "u" {
		label += fmt.Sprintf(" (%d%%)", *c)
	}
	return label
}

func positionName(p fpl.Position) string { return p.String() }

// fixtureText renders a team's matches in a gameweek: "ARS (H)", "ARS (H) + MCI (A)", or "no fixture".
func fixtureText(opps []fpl.Opponent, short map[int]string) string {
	if len(opps) == 0 {
		return "no fixture"
	}
	parts := make([]string, len(opps))
	for i, o := range opps {
		venue := "A"
		if o.Home {
			venue = "H"
		}
		name := short[o.Team]
		if name == "" {
			name = "?"
		}
		parts[i] = fmt.Sprintf("%s (%s)", name, venue)
	}
	return strings.Join(parts, " + ")
}
