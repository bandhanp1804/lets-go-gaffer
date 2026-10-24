package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// The methods below back the screens that are not a question to the assistant: the
// dashboard, the squad builder, and the model page.

// ClubView is a club for a filter drop-down.
type ClubView struct {
	Short string `json:"short"`
	Name  string `json:"name"`
}

// SessionView says whether the live FPL session can be used, without touching FPL.
type SessionView struct {
	Configured bool   `json:"configured"`
	State      string `json:"state"`                // "not configured", "valid", "expired" or "unknown expiry"
	ExpiresIn  string `json:"expires_in,omitempty"` // while valid and the expiry is known
}

// StatusView is the dashboard: where the season is, how fresh the data is, and what is configured.
type StatusView struct {
	Gameweek       int         `json:"gameweek"` // the upcoming gameweek, the one being decided for
	GameweekName   string      `json:"gameweek_name"`
	Deadline       string      `json:"deadline"`      // RFC 3339
	DeadlineIn     string      `json:"deadline_in"`   // "2d 5h", or "passed"
	LiveGameweek   int         `json:"live_gameweek"` // the gameweek being played now, 0 between gameweeks
	LastFinished   int         `json:"last_finished_gameweek"`
	HistoryThrough int         `json:"history_through_gameweek"` // the last gameweek the stored player history covers
	Players        int         `json:"players"`
	Clubs          []ClubView  `json:"clubs"`
	NewsArticles   int         `json:"news_articles"`
	NewsSearch     bool        `json:"news_search_available"` // an embedding key is configured
	TeamConfigured bool        `json:"team_configured"`
	Session        SessionView `json:"session"`
	Notes          []string    `json:"notes"` // problems with the data itself, such as history lagging a finished gameweek
}

// Status describes the season and the data. It reads no squad, so it works without a team id.
func (s *Service) Status(ctx context.Context) (StatusView, error) {
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return StatusView{}, err
	}
	now := s.deps.Now()
	v := StatusView{
		Gameweek: base.next, LastFinished: fpl.LastFinishedGameweek(base.gameweeks), HistoryThrough: base.asOf - 1,
		Players: len(base.players), NewsSearch: s.deps.Embedder != nil, TeamConfigured: s.cfg.TeamID != 0,
		Clubs: []ClubView{}, Notes: append([]string{}, base.notes...),
	}
	for _, g := range base.gameweeks {
		if g.IsCurrent && !g.Finished {
			v.LiveGameweek = g.ID
		}
		if g.ID == base.next {
			v.GameweekName, v.Deadline = g.Name, g.DeadlineTime
			if t, err := time.Parse(time.RFC3339, g.DeadlineTime); err == nil {
				v.DeadlineIn = countdown(t.Sub(now))
			}
		}
	}
	for _, t := range base.teams {
		v.Clubs = append(v.Clubs, ClubView{Short: t.ShortName, Name: t.Name})
	}
	if c, err := s.deps.Store.CountNews(ctx); err != nil {
		v.Notes = append(v.Notes, "The number of stored news articles could not be read.")
	} else {
		v.NewsArticles = c.Articles
	}
	_, token, err := s.who(ctx)
	if err != nil { // no team yet: there is no squad whose session could be reported
		token = ""
	}
	v.Session = s.sessionView(now, token)
	return v, nil
}

func (s *Service) sessionView(now time.Time, token string) SessionView {
	if s.deps.Live == nil || token == "" {
		return SessionView{State: "not configured"}
	}
	expiry, known := fpl.TokenExpiry(token)
	switch {
	case !known:
		return SessionView{Configured: true, State: "unknown expiry"}
	case !expiry.After(now):
		return SessionView{Configured: true, State: "expired"}
	}
	return SessionView{Configured: true, State: "valid", ExpiresIn: countdown(expiry.Sub(now))}
}

// countdown renders a duration to a moment as "2d 5h", "5h 10m" or "12 min"; a moment
// already past is "passed".
func countdown(d time.Duration) string {
	switch {
	case d <= 0:
		return "passed"
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// SquadRequest tunes BuildSquad.
type SquadRequest struct {
	Horizon int // gameweeks ahead to score; default 4, at most maxSquadHorizon
}

const maxSquadHorizon = 10

// SquadView is a squad built from scratch.
type SquadView struct {
	Gameweek       int          `json:"gameweek"`
	Horizon        int          `json:"horizon_gameweeks"`
	Formation      string       `json:"formation"`
	Cost           string       `json:"cost"`
	Budget         string       `json:"budget"`
	ExpectedPoints float64      `json:"expected_points"` // the XI per gameweek, captain counted twice
	Captain        string       `json:"captain"`
	ViceCaptain    string       `json:"vice_captain"`
	Starting       []PlayerView `json:"starting"`
	Bench          []PlayerView `json:"bench"`
	Notes          []string     `json:"notes"`
	XPBasis        string       `json:"xp_basis"` // what each player's "xp" is: an average over the horizon
}

// BuildSquad picks the best legal 15-player squad under the £100.0m budget, ranked by
// expected points over the next few gameweeks. It ignores the manager's own squad.
func (s *Service) BuildSquad(ctx context.Context, req SquadRequest) (SquadView, error) {
	if req.Horizon < 0 || req.Horizon > maxSquadHorizon {
		return SquadView{}, badInput("horizon must be between 1 and %d gameweeks", maxSquadHorizon)
	}
	horizon := req.Horizon
	if horizon == 0 {
		horizon = defaultHorizon
	}
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return SquadView{}, err
	}
	score := base.model.ScoreFrom(base.next, horizon)
	var cands []optimizer.Candidate
	for _, p := range base.players {
		if optimizer.Available(p) {
			cands = append(cands, optimizer.Candidate{Player: p, Cost: p.NowCost, Score: score(p)})
		}
	}
	best, err := optimizer.BestSquad(cands, optimizer.Budget)
	if err != nil {
		return SquadView{}, fmt.Errorf("build squad: %w", err)
	}
	advice, err := lineup.Advise(best.Squad.Players, lineup.Options{XP: score})
	if err != nil {
		return SquadView{}, err
	}
	v := SquadView{
		Gameweek: base.next, Horizon: horizon, Formation: advice.Formation,
		Cost: formatMoney(best.Cost), Budget: formatMoney(optimizer.Budget), ExpectedPoints: round1(advice.Total),
		Captain: advice.Captain.Player.WebName, ViceCaptain: advice.ViceCaptain.Player.WebName,
		Starting: []PlayerView{}, Bench: []PlayerView{}, Notes: joinNotes(base.notes), XPBasis: averageBasis(horizon),
	}
	for _, e := range advice.Starting {
		v.Starting = append(v.Starting, base.view(e.Player, e.XP))
	}
	for _, e := range advice.Bench {
		v.Bench = append(v.Bench, base.view(e.Player, e.XP))
	}
	return v, nil
}

// MetricsView is how well a way of scoring players ranked past gameweeks.
type MetricsView struct {
	Spearman float64 `json:"spearman"` // rank correlation with actual points; 1 is perfect, 0 is noise
	MAE      float64 `json:"mae"`      // mean absolute error, in points
	Top20    float64 `json:"top20"`    // mean actual points of the 20 players ranked highest
}

// ModelView is one way of scoring players.
type ModelView struct {
	Name string `json:"name"`
	MetricsView
}

// GameweekCompareView compares the old scorer with the model for one gameweek.
type GameweekCompareView struct {
	Gameweek int     `json:"gameweek"`
	Old      float64 `json:"old_spearman"`
	Model    float64 `json:"model_spearman"`
}

// ParamsView is a set of model settings.
type ParamsView struct {
	RateDecay    float64 `json:"rate_decay"`
	MinutesDecay float64 `json:"minutes_decay"`
	PriorGames   float64 `json:"prior_games"`
	FixtureSlope float64 `json:"fixture_slope"`
}

// SettingsView is a set of settings and how they scored over every judged gameweek.
type SettingsView struct {
	ParamsView
	MetricsView
}

// WalkForwardView is one honest out-of-sample step: settings tuned on earlier gameweeks,
// scored on one they never saw.
type WalkForwardView struct {
	Gameweek  int        `json:"gameweek"`
	TunedOn   string     `json:"tuned_on"` // "GW3-4"
	Settings  ParamsView `json:"settings"`
	Tuned     float64    `json:"tuned_spearman"`
	Default   float64    `json:"default_spearman"`
	OldScorer float64    `json:"old_scorer_spearman"`
}

// BacktestView is the model page: how the scoring model did on gameweeks already played.
type BacktestView struct {
	FirstGameweek int                   `json:"first_gameweek"`
	LastGameweek  int                   `json:"last_gameweek"`
	Models        []ModelView           `json:"models"`
	PerGameweek   []GameweekCompareView `json:"per_gameweek"`
	ModelWins     int                   `json:"model_wins"`
	Default       ParamsView            `json:"default_settings"`
	Best          []SettingsView        `json:"best_settings"`
	WalkForward   []WalkForwardView     `json:"walk_forward"`
	AvgTuned      float64               `json:"walk_forward_avg_tuned"`
	AvgDefault    float64               `json:"walk_forward_avg_default"`
	AvgOld        float64               `json:"walk_forward_avg_old_scorer"`
	Notes         []string              `json:"notes"`
}

func paramsView(p projection.Params) ParamsView {
	return ParamsView{RateDecay: p.Decay, MinutesDecay: p.MinutesDecay, PriorGames: p.PriorGames, FixtureSlope: p.FixtureSlope}
}

func metricsView(m projection.Metrics) MetricsView {
	return MetricsView{Spearman: round3(m.Spearman), MAE: round2(m.MAE), Top20: round2(m.TopMean)}
}

// Backtest reports how well each way of scoring players ranked gameweeks already
// played, using only the gameweeks before each. It takes seconds, so the result is kept
// until the stored history changes.
func (s *Service) Backtest(ctx context.Context) (BacktestView, error) {
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return BacktestView{}, err
	}
	key := fmt.Sprintf("%d/%d/%d", len(base.history), base.asOf, len(base.players))

	s.btMu.Lock()
	defer s.btMu.Unlock()
	if s.btView != nil && s.btKey == key {
		return *s.btView, nil
	}
	rep, err := projection.RunBacktest(ctx, base.players, base.history, base.fixtures)
	if errors.Is(err, projection.ErrTooLittleHistory) {
		return BacktestView{}, fmt.Errorf("%w (the model page needs at least %d played gameweeks)", err, projection.BacktestFirst)
	}
	if err != nil {
		return BacktestView{}, err
	}

	v := BacktestView{
		FirstGameweek: rep.First, LastGameweek: rep.Last, ModelWins: rep.ModelWins, Default: paramsView(projection.Default),
		Models: []ModelView{}, PerGameweek: []GameweekCompareView{}, Best: []SettingsView{}, WalkForward: []WalkForwardView{},
		AvgTuned: round3(rep.AvgTuned), AvgDefault: round3(rep.AvgDefault), AvgOld: round3(rep.AvgOld), Notes: []string{},
	}
	for _, m := range rep.Models {
		v.Models = append(v.Models, ModelView{Name: m.Name, MetricsView: metricsView(m.Metrics)})
	}
	for _, g := range rep.PerGameweek {
		v.PerGameweek = append(v.PerGameweek, GameweekCompareView{Gameweek: g.Gameweek, Old: round3(g.Old), Model: round3(g.Model)})
	}
	for _, b := range rep.Best {
		v.Best = append(v.Best, SettingsView{ParamsView: paramsView(b.Params), MetricsView: metricsView(b.Metrics)})
	}
	for _, w := range rep.WalkForward {
		v.WalkForward = append(v.WalkForward, WalkForwardView{
			Gameweek: w.Gameweek, TunedOn: fmt.Sprintf("GW%d-%d", w.TunedFrom, w.TunedTo), Settings: paramsView(w.Params),
			Tuned: round3(w.Tuned), Default: round3(w.Default), OldScorer: round3(w.OldRho),
		})
	}
	if weeks := rep.Last - rep.First + 1; weeks < 10 {
		v.Notes = append(v.Notes, fmt.Sprintf("Only %d gameweeks are judged, so small differences between settings are noise; the gap to the old scorer is the reliable part.", weeks))
	}
	v.Notes = append(v.Notes, "Availability is left out of the backtest: today's injury list cannot be applied to past gameweeks.")
	s.btKey, s.btView = key, &v
	return v, nil
}

// averageBasis says what an "xp" that averages over a horizon is, so no one has to guess why
// it differs from a figure for one gameweek.
func averageBasis(horizon int) string {
	return fmt.Sprintf("each player's expected points per gameweek, averaged over the next %d %s", horizon, plural(horizon, "gameweek", "gameweeks"))
}

// PlayersXPBasis is the basis of the "xp" in player lists.
var PlayersXPBasis = averageBasis(defaultHorizon)
