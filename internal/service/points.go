package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// PointsPlayerView is one player's points in the gameweek being scored.
type PointsPlayerView struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Team       string `json:"team"`
	Position   string `json:"position"`
	Points     int    `json:"points"`     // the player's own points, before the multiplier
	Multiplier int    `json:"multiplier"` // 2 for the captain, 3 with Triple Captain, 0 on the bench
	Counted    int    `json:"counted"`    // Points x Multiplier: what he adds to the total
	Minutes    int    `json:"minutes"`    // minutes played so far
	Captain    bool   `json:"captain"`
	Vice       bool   `json:"vice_captain"`
	Bench      bool   `json:"bench"`      // started on the bench (and did not come on)
	SubbedOn   bool   `json:"subbed_on"`  // a bench player automatically brought on
	SubbedOff  bool   `json:"subbed_off"` // a starter who did not play and was replaced
	// Matches is where the points came from, match by match, as FPL shows them. A match that
	// has not started is listed with no statistics.
	Matches []MatchBreakdownView `json:"matches"`
}

// MatchBreakdownView is one match of a player's gameweek.
type MatchBreakdownView struct {
	Home      string        `json:"home"` // club names
	Away      string        `json:"away"`
	HomeScore *int          `json:"home_score"` // null before kick-off
	AwayScore *int          `json:"away_score"`
	Status    string        `json:"status"`  // "FT", a minute such as "67'", or "Not started"
	Kickoff   string        `json:"kickoff"` // RFC 3339, so the page can show it in the reader's time
	Stats     []StatRowView `json:"stats"`
	Points    int           `json:"points"` // the match's points: the sum of the rows
}

// StatRowView is one line of a breakdown: a statistic, how many, and the points it earned.
type StatRowView struct {
	Label  string `json:"label"`
	Value  int    `json:"value"`
	Points int    `json:"points"`
}

// statLabels name FPL's scoring statistics the way FPL's own page does.
var statLabels = map[string]string{
	"minutes": "Minutes played", "goals_scored": "Goals scored", "assists": "Assists", "clean_sheets": "Clean sheets",
	"goals_conceded": "Goals conceded", "own_goals": "Own goals", "penalties_saved": "Penalties saved",
	"penalties_missed": "Penalties missed", "yellow_cards": "Yellow cards", "red_cards": "Red cards", "saves": "Saves",
	"bonus": "Bonus", "defensive_contribution": "Defensive contribution",
}

func statLabel(id string) string {
	if l, ok := statLabels[id]; ok {
		return l
	}
	l := strings.ReplaceAll(id, "_", " ")
	if l == "" {
		return id
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

// matchesFor lists a player's matches in a gameweek with what he scored in each. Every match his
// club plays is included, so a double gameweek shows two and a match yet to be played shows none
// of the statistics.
func matchesFor(base *baseSnap, teamID, gw int, explain []fpl.LiveMatch) []MatchBreakdownView {
	byFixture := make(map[int][]fpl.LiveStat, len(explain))
	for _, m := range explain {
		byFixture[m.Fixture] = m.Stats
	}
	names := make(map[int]string, len(base.teams))
	for _, t := range base.teams {
		names[t.ID] = t.Name
	}
	out := []MatchBreakdownView{}
	for _, f := range base.fixtures {
		if f.Event != gw || (f.HomeTeam != teamID && f.AwayTeam != teamID) {
			continue
		}
		m := MatchBreakdownView{Home: names[f.HomeTeam], Away: names[f.AwayTeam], HomeScore: f.HomeScore, AwayScore: f.AwayScore, Kickoff: f.KickoffTime, Stats: []StatRowView{}}
		switch {
		case f.Finished:
			m.Status = "FT"
		case f.Started:
			m.Status = fmt.Sprintf("%d'", f.Minutes)
		default:
			m.Status = "Not started"
		}
		for _, st := range byFixture[f.ID] {
			if st.Points == 0 && st.Value == 0 {
				continue
			}
			m.Stats = append(m.Stats, StatRowView{Label: statLabel(st.Identifier), Value: st.Value, Points: st.Points})
			m.Points += st.Points
		}
		out = append(out, m)
	}
	return out
}

// PointsView is how the team is scoring in the gameweek being played, or in the last one
// finished. It comes from public data, so it works without a login, and it describes the
// team that was picked at that deadline, which may not be the squad after later transfers.
type PointsView struct {
	Gameweek int    `json:"gameweek"`
	InPlay   bool   `json:"in_play"` // the gameweek is not finished: the figures are provisional
	Started  bool   `json:"started"` // at least one player has played minutes
	Gross    int    `json:"gross_points"`
	Hit      int    `json:"transfer_hit"`
	Net      int    `json:"net_points"` // Gross minus the hit: the gameweek's score
	OnBench  int    `json:"points_on_bench"`
	Chip     string `json:"chip,omitempty"` // the chip played that gameweek

	SeasonPoints int `json:"season_points"` // as FPL reports it for this gameweek
	OverallRank  int `json:"overall_rank"`

	Players []PointsPlayerView `json:"players"` // the starting XI by position, then the bench
	Note    string             `json:"note"`
}

type pointsKey struct{ team, gw int }

type pointsEntry struct {
	view *PointsView
	at   time.Time
}

const (
	pointsTTLInPlay   = time.Minute      // points move as matches are played
	pointsTTLFinished = 10 * time.Minute // a finished gameweek only changes when FPL confirms bonus points
	maxCachedPoints   = 128
)

// scoringGameweek is the gameweek whose points are shown: the one being played, or else the
// last one finished. It is 0 before the season starts.
func scoringGameweek(gws []fpl.Gameweek) (gw int, inPlay bool) {
	for _, g := range gws {
		if g.IsCurrent && !g.Finished {
			return g.ID, true
		}
	}
	return fpl.LastFinishedGameweek(gws), false
}

// pointsFor reads the team's points for the scoring gameweek. It never fails the caller:
// without points the squad is still worth showing, so a problem becomes a note.
func (s *Service) pointsFor(ctx context.Context, base *baseSnap, team int) (*PointsView, string) {
	gw, inPlay := scoringGameweek(base.gameweeks)
	if gw == 0 {
		return nil, ""
	}
	key := pointsKey{team, gw}
	ttl := pointsTTLFinished
	if inPlay {
		ttl = pointsTTLInPlay
	}
	s.ptMu.Lock()
	if e, ok := s.points[key]; ok && s.deps.Now().Sub(e.at) < ttl {
		s.ptMu.Unlock()
		return e.view, ""
	}
	s.ptMu.Unlock()

	picks, err := s.deps.FPL.FetchPicks(ctx, team, gw)
	if err != nil {
		return nil, fmt.Sprintf("GW%d points could not be read from FPL right now.", gw)
	}
	live, err := s.deps.FPL.FetchLive(ctx, gw)
	if err != nil {
		return nil, fmt.Sprintf("GW%d points could not be read from FPL right now.", gw)
	}
	v := scorePicks(gw, inPlay, picks, live, base)

	s.ptMu.Lock()
	if s.points == nil || len(s.points) >= maxCachedPoints {
		s.points = map[pointsKey]pointsEntry{}
	}
	s.points[key] = pointsEntry{view: v, at: s.deps.Now()}
	s.ptMu.Unlock()
	return v, ""
}

// scorePicks adds up a gameweek the way FPL does: each player's points times his multiplier
// (captain doubled, the bench not counted unless Bench Boost), automatic substitutions applied
// when FPL has confirmed them, minus the transfer hit.
func scorePicks(gw int, inPlay bool, picks *fpl.Picks, live *fpl.LiveEvent, base *baseSnap) *PointsView {
	type line struct {
		points, minutes int
		explain         []fpl.LiveMatch
	}
	lines := make(map[int]line, len(live.Elements))
	for _, e := range live.Elements {
		lines[e.ID] = line{e.Stats.TotalPoints, e.Stats.Minutes, e.Explain}
	}
	started := false // whether any of this team's own players has played, not anyone in the league
	for _, p := range picks.Picks {
		started = started || lines[p.Element].minutes > 0
	}
	subOn, subOff := map[int]bool{}, map[int]bool{}
	for _, a := range picks.AutomaticSubs {
		subOn[a.In], subOff[a.Out] = true, true
	}
	byID := make(map[int]fpl.Player, len(base.players))
	for _, p := range base.players {
		byID[p.ID] = p
	}

	// A captain who played no minutes at all passes the armband to the vice-captain, but only
	// once the gameweek is over: before then he may simply not have played yet.
	capID, viceID := 0, 0
	for _, p := range picks.Picks {
		if p.IsCaptain {
			capID = p.Element
		}
		if p.IsViceCaptain {
			viceID = p.Element
		}
	}
	armbandMoves := !inPlay && capID != 0 && lines[capID].minutes == 0 && lines[viceID].minutes > 0

	v := &PointsView{Gameweek: gw, InPlay: inPlay, Started: started, Players: []PointsPlayerView{}, Hit: picks.EntryHistory.EventTransfersCost,
		SeasonPoints: picks.EntryHistory.TotalPoints, OverallRank: picks.EntryHistory.OverallRank}
	if picks.ActiveChip != nil {
		v.Chip = *picks.ActiveChip
	}
	ordered := append([]fpl.Pick(nil), picks.Picks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	for _, p := range ordered {
		l := lines[p.Element]
		mult := p.Multiplier
		switch {
		case subOff[p.Element]:
			mult = 0
		case subOn[p.Element] && mult == 0:
			mult = 1
		}
		if armbandMoves {
			switch p.Element {
			case capID:
				mult = min(mult, 1)
			case viceID:
				mult = max(mult, 2)
			}
		}
		pl := byID[p.Element]
		pv := PointsPlayerView{
			ID: p.Element, Name: pl.WebName, Team: base.short[pl.TeamID], Position: positionName(p.ElementType),
			Points: l.points, Multiplier: mult, Counted: l.points * mult, Minutes: l.minutes,
			Captain: p.IsCaptain, Vice: p.IsViceCaptain, Bench: p.Position > 11 && !subOn[p.Element],
			SubbedOn: subOn[p.Element], SubbedOff: subOff[p.Element],
			Matches: matchesFor(base, pl.TeamID, gw, l.explain),
		}
		v.Gross += pv.Counted
		if pv.Bench {
			v.OnBench += pv.Points
		}
		v.Players = append(v.Players, pv)
	}
	v.Net = v.Gross - v.Hit
	switch {
	case !inPlay:
		v.Note = fmt.Sprintf("GW%d is finished. These are its final points.", gw)
	case !started:
		v.Note = fmt.Sprintf("GW%d is being played, but none of your players has taken the field yet, so you are on zero (plus any transfer hit).", gw)
	default:
		v.Note = fmt.Sprintf("GW%d is being played: the total is provisional. Bonus points are added when each match ends, and automatic substitutions appear once FPL confirms them.", gw)
	}
	return v
}

// pointsCache is the small cache pointsFor keeps, created with the service.
type pointsCache struct {
	ptMu   sync.Mutex
	points map[pointsKey]pointsEntry
}
