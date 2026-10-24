// Package service answers the read-only questions a manager asks: how is my squad,
// who should start, which transfers are worth making, who are the best players, and
// what is the news. It is the one place that combines the database, FPL's API, the
// projection model and the optimizers; the assistant's tools and the HTTP API both
// sit on top of it.
//
// Everything here is read-only. Nothing changes the manager's FPL team.
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

const (
	// snapshotTTL is how long fetched data is reused. One question usually triggers
	// several tool calls, which should share one fetch; a later question should not
	// see data from an hour ago.
	snapshotTTL = 3 * time.Minute

	defaultHorizon  = 4
	defaultPlayers  = 10
	maxPlayers      = 25
	defaultNews     = 5
	maxNews         = 10
	soonToExpire    = 10 * time.Minute
	defaultTopMoves = 5
)

// InputError means the request itself was wrong (an unknown club, a negative price),
// not that the system failed. Its message is written for the person who asked, so a
// caller may show it and should answer with a client error rather than a server one.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func badInput(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// ErrNewsUnavailable means no embedder is configured, so news cannot be searched.
var ErrNewsUnavailable = errors.New("news search is unavailable: no embedding key is configured")

// Store is the part of the database the service reads.
type Store interface {
	ListPlayers(ctx context.Context) ([]fpl.Player, error)
	ListTeams(ctx context.Context) ([]fpl.Team, error)
	ListHistory(ctx context.Context) ([]fpl.PlayerHistory, error)
	StartingPrices(ctx context.Context, playerIDs []int) (map[int]int, error)
	FindPlayers(ctx context.Context, name string) ([]fpl.Player, error)
	SearchNews(ctx context.Context, query []float32, f db.SearchFilter) ([]db.Hit, error)
	CountNews(ctx context.Context) (db.NewsCounts, error)
}

// FPL is FPL's public API.
type FPL interface {
	FetchEntry(ctx context.Context, teamID int) (*fpl.Entry, error)
	FetchPicks(ctx context.Context, teamID, gameweek int) (*fpl.Picks, error)
	FetchHistory(ctx context.Context, teamID int) (*fpl.History, error)
	FetchTransfers(ctx context.Context, teamID int) ([]fpl.Transfer, error)
	FetchFixtures(ctx context.Context) (fpl.Fixtures, error)
	FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error)
}

// Live reads the logged-in manager's squad as it stands now. It may be nil.
type Live interface {
	FetchMyTeam(ctx context.Context, teamID int) (*fpl.MyTeam, error)
}

// Embedder turns a question into a vector. It may be nil.
type Embedder interface {
	Embed(ctx context.Context, texts []string, kind embed.InputType) ([][]float32, error)
}

// Deps are the service's collaborators.
type Deps struct {
	Store    Store
	FPL      FPL
	Live     Live   // optional
	Session  string // the session token, read only for its expiry and never printed
	Embedder Embedder
	Now      func() time.Time // default time.Now
}

// Config identifies the manager.
type Config struct {
	TeamID int
}

// Service is safe for concurrent use.
type Service struct {
	deps Deps
	cfg  Config

	mu      sync.Mutex
	base    *baseSnap
	baseAt  time.Time
	squad   *squadSnap
	squadAt time.Time

	btMu   sync.Mutex // the backtest takes seconds, so it has its own lock and cache
	btKey  string
	btView *BacktestView
}

// New returns a Service.
func New(deps Deps, cfg Config) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, cfg: cfg}
}

// baseSnap is the season's data, shared by every question.
type baseSnap struct {
	players   []fpl.Player
	teams     []fpl.Team
	short     map[int]string // team id -> short name
	history   []fpl.PlayerHistory
	fixtures  fpl.Fixtures
	gameweeks []fpl.Gameweek
	asOf      int      // the last gameweek the stored history covers
	next      int      // the gameweek being decided for: the one whose deadline has not passed
	notes     []string // problems with the data itself, shown with every squad answer
	model     *projection.Model
}

// squadSnap is the manager's squad and money.
type squadSnap struct {
	team  myteam.Team
	fin   myteam.Finances
	live  bool
	notes []string
}

func (s *Service) baseSnapshot(ctx context.Context) (*baseSnap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base != nil && s.deps.Now().Sub(s.baseAt) < snapshotTTL {
		return s.base, nil
	}
	players, err := s.deps.Store.ListPlayers(ctx)
	if err != nil {
		return nil, err
	}
	teams, err := s.deps.Store.ListTeams(ctx)
	if err != nil {
		return nil, err
	}
	history, err := s.deps.Store.ListHistory(ctx)
	if err != nil {
		return nil, err
	}
	if len(players) == 0 || len(history) == 0 {
		return nil, errors.New("no players or history are stored; the data needs a sync first")
	}
	fixtures, err := s.deps.FPL.FetchFixtures(ctx)
	if err != nil {
		return nil, err
	}
	boot, err := s.deps.FPL.FetchBootstrap(ctx)
	if err != nil {
		return nil, err
	}
	// The gameweek being decided for comes from FPL's schedule, never from how far the
	// stored data goes: once a deadline passes its gameweek is locked even though its
	// history is not in yet.
	next := fpl.UpcomingGameweek(boot.Gameweeks)
	if next == 0 {
		return nil, errors.New("there is no upcoming gameweek (the season may be over)")
	}
	asOf := projection.AsOf(history)
	model, err := projection.New(players, history, fixtures, projection.Default, asOf)
	if err != nil {
		return nil, err
	}
	var notes []string
	if n := projection.HistoryNote(asOf, fpl.LastFinishedGameweek(boot.Gameweeks)); n != "" {
		notes = append(notes, n)
	}
	short := make(map[int]string, len(teams))
	for _, t := range teams {
		short[t.ID] = t.ShortName
	}
	s.base = &baseSnap{players: players, teams: teams, short: short, history: history, fixtures: fixtures, gameweeks: boot.Gameweeks, asOf: asOf, next: next, notes: notes, model: model}
	s.baseAt = s.deps.Now()
	return s.base, nil
}

func (s *Service) squadSnapshot(ctx context.Context) (*baseSnap, *squadSnap, error) {
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.squad != nil && s.deps.Now().Sub(s.squadAt) < snapshotTTL {
		return base, s.squad, nil
	}
	if s.cfg.TeamID == 0 {
		return nil, nil, errors.New("no FPL team id is configured")
	}

	public, err := myteam.Load(ctx, s.deps.FPL, s.cfg.TeamID, base.players)
	if err != nil {
		return nil, nil, err
	}
	snap := &squadSnap{}

	live, note := s.tryLive(ctx)
	if note != "" {
		snap.notes = append(snap.notes, note)
	}
	if live != nil {
		team, fin, err := myteam.WithLiveSquad(public, live, base.players)
		if err != nil {
			return nil, nil, err
		}
		snap.team, snap.fin, snap.live = team, fin, true
	} else {
		ids := make([]int, len(public.Squad.Players))
		for i, p := range public.Squad.Players {
			ids[i] = p.ID
		}
		start, err := s.deps.Store.StartingPrices(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		fin, err := myteam.LoadFinances(ctx, s.deps.FPL, s.cfg.TeamID, public, start)
		if err != nil {
			return nil, nil, err
		}
		snap.team, snap.fin = public, fin
		snap.notes = append(snap.notes, fmt.Sprintf(
			"This squad is as of the GW%d deadline: FPL hides transfers made since. Anything the manager has changed since then is not reflected.",
			public.Event))
	}
	s.squad, s.squadAt = snap, s.deps.Now()
	return base, snap, nil
}

// tryLive reads the live squad if a session is configured. It never fails: it
// returns nil and a note saying why. No note contains the token.
func (s *Service) tryLive(ctx context.Context) (*fpl.MyTeam, string) {
	if s.deps.Live == nil || strings.TrimSpace(s.deps.Session) == "" {
		return nil, ""
	}
	now := s.deps.Now()
	expiry, known := fpl.TokenExpiry(s.deps.Session)
	if known && !expiry.After(now) {
		return nil, fmt.Sprintf("The FPL session expired %s ago, so only public data is used.", ago(now.Sub(expiry)))
	}
	live, err := s.deps.Live.FetchMyTeam(ctx, s.cfg.TeamID)
	switch {
	case errors.Is(err, fpl.ErrSessionRejected):
		return nil, "FPL rejected the session (it has probably expired), so only public data is used."
	case err != nil:
		return nil, fmt.Sprintf("The live squad could not be read from FPL (%v), so only public data is used.", err)
	}
	note := "Squad read live from FPL (includes transfers made since the last deadline)."
	if known && expiry.Sub(now) < soonToExpire {
		note += " The session expires within minutes."
	}
	return live, note
}

func (s *Service) source(sq *squadSnap) string {
	if sq.live {
		return "live"
	}
	return "public"
}

// asOf states when a squad is from, in words a consumer can quote without working
// anything out: a model that has to infer "which deadline" from other fields will,
// sooner or later, get it wrong.
func (s *Service) asOf(sq *squadSnap) string {
	if sq.live {
		return "now (live from FPL)"
	}
	return fmt.Sprintf("the GW%d deadline", sq.team.Event)
}

// view builds a PlayerView. xp is the caller's chosen expected-points figure.
func (b *baseSnap) view(p fpl.Player, xp float64) PlayerView {
	return PlayerView{
		ID: p.ID, Name: p.WebName, Team: b.short[p.TeamID], Position: positionName(p.Position),
		Price: formatMoney(p.NowCost), XP: round1(xp), Form: p.Form, TotalPoints: p.TotalPoints,
		Status: statusText(p), News: p.News,
		Fixture: fixtureText(b.fixtures.Opponents(p.TeamID, b.next), b.short),
	}
}

func joinNotes(groups ...[]string) []string {
	out := []string{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func nameOf(players []fpl.Player, id int) string {
	for _, p := range players {
		if p.ID == id {
			return p.WebName
		}
	}
	return ""
}

// MyTeam is the manager's squad with expected points for the next gameweek.
func (s *Service) MyTeam(ctx context.Context) (MyTeamView, error) {
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return MyTeamView{}, err
	}
	xp := base.model.ScoreFrom(base.next, 1)
	started := make(map[int]bool, 11)
	for _, id := range sq.team.Squad.Starting {
		started[id] = true
	}
	v := MyTeamView{
		Manager: strings.TrimSpace(sq.team.Entry.FirstName + " " + sq.team.Entry.LastName), TeamName: sq.team.Entry.Name,
		Gameweek: base.next, Source: s.source(sq), AsOf: s.asOf(sq),
		Captain: nameOf(sq.team.Squad.Players, sq.team.Squad.Captain), ViceCaptain: nameOf(sq.team.Squad.Players, sq.team.Squad.ViceCaptain),
		Bank: formatMoney(sq.fin.Bank), FreeTransfers: sq.fin.Free.Next, TransfersMade: sq.fin.PendingTransfers,
		HitSoFar: sq.fin.PendingHit, ChipsAvailable: sq.fin.AvailableChips, Notes: joinNotes(base.notes, sq.notes),
	}
	for _, p := range sq.team.Squad.Players {
		pv := base.view(p, xp(p))
		pv.SellPrice = formatMoney(sq.fin.Sell[p.ID])
		if started[p.ID] {
			v.Starting = append(v.Starting, pv)
		} else {
			v.Bench = append(v.Bench, pv)
		}
	}
	return v, nil
}

// Lineup recommends the best XI for the upcoming gameweek.
func (s *Service) Lineup(ctx context.Context) (LineupView, error) {
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return LineupView{}, err
	}
	xp := base.model.ScoreFrom(base.next, 1)
	advice, err := lineup.Advise(sq.team.Squad.Players, lineup.Options{XP: xp})
	if err != nil {
		return LineupView{}, err
	}

	entryView := func(e lineup.Entry) PlayerView { return base.view(e.Player, e.XP) }
	v := LineupView{
		Gameweek: base.next, Source: s.source(sq), AsOf: s.asOf(sq), Formation: advice.Formation, ExpectedPoints: round1(advice.Total),
		Captain: advice.Captain.Player.WebName, ViceCaptain: advice.ViceCaptain.Player.WebName,
		CaptainLead: round2(advice.Captain.XP - advice.ViceCaptain.XP), Notes: joinNotes(base.notes, sq.notes),
	}
	var ids []int
	for _, e := range advice.Starting {
		v.Starting = append(v.Starting, entryView(e))
		ids = append(ids, e.Player.ID)
	}
	for _, e := range advice.Bench {
		v.Bench = append(v.Bench, entryView(e))
	}
	for _, c := range advice.CloseCalls {
		v.CloseCalls = append(v.CloseCalls, CloseCallView{Bench: c.Bench.Player.WebName, Displaces: c.Displaces.Player.WebName, Margin: round2(c.Margin)})
	}
	for _, f := range advice.Formations {
		if f.Formation != advice.Formation {
			v.OtherFormations = append(v.OtherFormations, FormationView{Formation: f.Formation, Delta: round1(f.Delta)})
		}
	}
	if sq.live {
		v.CurrentLineupIs = "the lineup currently set on FPL"
	} else {
		v.CurrentLineupIs = fmt.Sprintf("the GW%d lineup (as of that deadline)", sq.team.Event)
	}
	in, out := lineup.Changes(sq.team.Squad.Starting, ids)
	v.SameAsCurrent = len(in) == 0 && len(out) == 0
	for _, id := range in {
		v.PlayersIn = append(v.PlayersIn, nameOf(sq.team.Squad.Players, id))
	}
	for _, id := range out {
		v.PlayersOut = append(v.PlayersOut, nameOf(sq.team.Squad.Players, id))
	}
	return v, nil
}

// TransfersRequest tunes Transfers. The zero value means: this week's transfers,
// with the free transfers FPL gives, looking four gameweeks ahead.
type TransfersRequest struct {
	Wildcard bool // plan a Wildcard or Free Hit: unlimited moves, no hit
	Free     *int // override the free transfers available
	Horizon  int  // gameweeks a gain is assumed to last; default 4
	Max      int  // most players to change in a normal plan; default 2
	TopN     int  // suggestions to return; default 5
}

// Transfers suggests moves, weighing each against the points hit it costs.
func (s *Service) Transfers(ctx context.Context, req TransfersRequest) (TransfersView, error) {
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return TransfersView{}, err
	}
	horizon := req.Horizon
	if horizon < 1 {
		horizon = defaultHorizon
	}
	free := sq.fin.Free.Next
	if req.Free != nil {
		free = max(*req.Free, 0)
	}
	topN := req.TopN
	if topN < 1 {
		topN = defaultTopMoves
	}
	in := transfers.Input{Squad: sq.team.Squad.Players, Sell: sq.fin.Sell, Bank: sq.fin.Bank, Pool: base.players}
	opts := transfers.Options{Score: base.model.ScoreFrom(base.next, horizon), FreeTransfers: free, MaxTransfers: req.Max, Horizon: horizon, TopN: topN}

	var found []transfers.Suggestion
	if req.Wildcard {
		w, err := transfers.Wildcard(in, opts)
		if err != nil {
			return TransfersView{}, err
		}
		if w.Transfers > 0 {
			found = []transfers.Suggestion{w}
		}
	} else if found, err = transfers.Suggest(in, opts); err != nil {
		return TransfersView{}, err
	}

	v := TransfersView{
		Gameweek: base.next, Source: s.source(sq), AsOf: s.asOf(sq), Wildcard: req.Wildcard, FreeTransfers: free, Bank: formatMoney(sq.fin.Bank),
		Horizon: horizon, PendingTransfers: sq.fin.PendingTransfers, PendingHit: sq.fin.PendingHit,
		Notes: joinNotes(base.notes, sq.notes), Suggestions: []SuggestionView{},
	}
	if !sq.live {
		v.Notes = append(v.Notes, "Selling prices are computed from FPL's rule (half of any price rise is kept, rounded down); the FPL transfers page shows the exact figures.")
	}
	for _, sg := range found {
		sv := SuggestionView{
			Transfers: sg.Transfers, Hit: sg.Hit, GainPerGameweek: round1(sg.ScoreGain),
			NetPoints: round1(sg.Net), BankAfter: formatMoney(sg.BankAfter),
		}
		for _, m := range sg.Moves {
			out := base.view(m.Out, m.OutScore)
			out.SellPrice = formatMoney(m.SellPrice)
			in := base.view(m.In, m.InScore)
			sv.Moves = append(sv.Moves, MoveView{Summary: MoveSummary(out, in), Out: out, In: in})
		}
		v.Suggestions = append(v.Suggestions, sv)
	}
	for i := range v.Suggestions { // computed here so the model never has to subtract two nets
		v.Suggestions[i].NetVsFirst = round1(found[i].Net - found[0].Net)
	}
	return v, nil
}

// PlayerQuery filters and ranks players. Prices are in pounds millions.
type PlayerQuery struct {
	Position string  // GKP, DEF, MID, FWD (or goalkeeper, defender, midfielder, forward)
	Team     string  // club short name ("ARS") or full name
	Name     string  // an exact player name
	Status   string  // available (default), doubtful, injured, suspended, unavailable, or any
	SortBy   string  // xp (default), form, price, points
	MaxPrice float64 // 0 means no ceiling
	MinPrice float64
	Limit    int // default 10, at most 25
}

// FindPlayers answers structured questions: the best midfielders under a price, who
// is injured at a club, and so on. Expected points are averaged over the next four
// gameweeks and already discounted by the chance of playing.
func (s *Service) FindPlayers(ctx context.Context, q PlayerQuery) ([]PlayerView, error) {
	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	pos, err := parsePosition(q.Position)
	if err != nil {
		return nil, err
	}
	teamID, err := base.teamID(q.Team)
	if err != nil {
		return nil, err
	}
	status, err := parseStatus(q.Status)
	if err != nil {
		return nil, err
	}
	sortBy := strings.ToLower(strings.TrimSpace(q.SortBy))
	if sortBy == "" {
		sortBy = "xp"
	}
	if sortBy != "xp" && sortBy != "form" && sortBy != "price" && sortBy != "points" {
		return nil, badInput("sort_by %q is not one of xp, form, price, points", q.SortBy)
	}
	if q.MaxPrice < 0 || q.MinPrice < 0 {
		return nil, badInput("prices cannot be negative")
	}
	limit := q.Limit
	if limit < 1 {
		limit = defaultPlayers
	}
	limit = min(limit, maxPlayers)

	pool := base.players
	if name := strings.TrimSpace(q.Name); name != "" {
		if pool, err = s.deps.Store.FindPlayers(ctx, name); err != nil {
			return nil, err
		}
	}
	xp := base.model.ScoreFrom(base.next, defaultHorizon)
	type scored struct {
		p  fpl.Player
		xp float64
	}
	var hits []scored
	maxP, minP := int(q.MaxPrice*10+0.5), int(q.MinPrice*10+0.5)
	for _, p := range pool {
		switch {
		case pos != 0 && p.Position != pos,
			teamID != 0 && p.TeamID != teamID,
			status != "" && !statusMatches(p, status),
			q.MaxPrice > 0 && p.NowCost > maxP,
			q.MinPrice > 0 && p.NowCost < minP:
			continue
		}
		hits = append(hits, scored{p, xp(p)})
	}
	form := func(p fpl.Player) float64 { f, _ := strconv.ParseFloat(p.Form, 64); return f }
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch sortBy {
		case "form":
			if form(a.p) != form(b.p) {
				return form(a.p) > form(b.p)
			}
		case "price":
			if a.p.NowCost != b.p.NowCost {
				return a.p.NowCost < b.p.NowCost
			}
		case "points":
			if a.p.TotalPoints != b.p.TotalPoints {
				return a.p.TotalPoints > b.p.TotalPoints
			}
		default:
			if a.xp != b.xp {
				return a.xp > b.xp
			}
		}
		return a.p.ID < b.p.ID
	})
	out := []PlayerView{}
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, base.view(h.p, h.xp))
	}
	return out, nil
}

func parsePosition(s string) (fpl.Position, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return 0, nil
	case "gkp", "gk", "goalkeeper", "goalkeepers":
		return fpl.GKP, nil
	case "def", "defender", "defenders":
		return fpl.DEF, nil
	case "mid", "midfielder", "midfielders":
		return fpl.MID, nil
	case "fwd", "forward", "forwards", "striker":
		return fpl.FWD, nil
	}
	return 0, badInput("position %q is not one of GKP, DEF, MID, FWD", s)
}

// parseStatus returns "" for "any", and defaults to "a" (available).
func parseStatus(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "available":
		return "a", nil
	case "any", "all":
		return "", nil
	case "doubtful":
		return "d", nil
	case "injured":
		return "i", nil
	case "suspended":
		return "s", nil
	case "unavailable":
		return "u", nil
	}
	return "", badInput("status %q is not one of available, doubtful, injured, suspended, unavailable, any", s)
}

func statusMatches(p fpl.Player, status string) bool { return p.Status == status }

func (b *baseSnap) teamID(name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, nil
	}
	for _, t := range b.teams {
		if strings.EqualFold(t.ShortName, name) || strings.EqualFold(t.Name, name) {
			return t.ID, nil
		}
	}
	return 0, badInput("no club named %q (use a short name such as ARS or the full name)", name)
}

// NewsQuery searches stored news by meaning.
type NewsQuery struct {
	Query  string // what to look for, in plain words
	Player string // restrict to stories about this player (exact name)
	Days   int    // only stories from the last N days; 0 means any age
	Limit  int    // default 5, at most 10
}

// SearchNews finds the stories closest in meaning to the question. A player's
// official FPL note counts only while it is that player's current note.
func (s *Service) SearchNews(ctx context.Context, q NewsQuery) ([]NewsView, error) {
	if s.deps.Embedder == nil {
		return nil, ErrNewsUnavailable
	}
	query := strings.TrimSpace(q.Query)
	switch {
	case query == "":
		return nil, badInput("a news query is required")
	case q.Days < 0:
		return nil, badInput("days cannot be negative")
	}
	limit := q.Limit
	if limit < 1 {
		limit = defaultNews
	}
	limit = min(limit, maxNews)

	base, err := s.baseSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	filter := db.SearchFilter{Limit: limit}
	if name := strings.TrimSpace(q.Player); name != "" {
		matches, err := s.deps.Store.FindPlayers(ctx, name)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
			return nil, badInput("no player named %q", name)
		case 1:
			filter.PlayerIDs = []int{matches[0].ID}
		default:
			opts := make([]string, len(matches))
			for i, m := range matches {
				opts[i] = fmt.Sprintf("%s (%s)", m.WebName, base.short[m.TeamID])
			}
			return nil, badInput("%q matches %d players: %s; use the exact web name", name, len(matches), strings.Join(opts, ", "))
		}
	}
	now := s.deps.Now()
	if q.Days > 0 {
		filter.Since = now.Add(-time.Duration(q.Days) * 24 * time.Hour)
	}

	// A question is embedded as a Query, not a Document: Voyage embeds them differently.
	vecs, err := s.deps.Embedder.Embed(ctx, []string{query}, embed.Query)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}
	hits, err := s.deps.Store.SearchNews(ctx, vecs[0], filter)
	if err != nil {
		return nil, err
	}
	out := []NewsView{}
	for _, h := range hits {
		nv := NewsView{
			Title: h.Title, Content: h.Content, Source: h.Source, URL: h.URL,
			Published: h.PublishedAt.UTC().Format(time.RFC3339), Age: age(now, h.PublishedAt), Similarity: round2(h.Similarity),
		}
		for _, id := range h.PlayerIDs {
			if n := nameOf(base.players, id); n != "" {
				nv.Players = append(nv.Players, n)
			}
		}
		out = append(out, nv)
	}
	return out, nil
}
