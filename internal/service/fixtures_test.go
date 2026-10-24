package service_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

const teamID = 4321

var testNow = time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)

// slotPosition: slot 1 and 12 GKP, 2-5 and 13 DEF, 6-9 and 14 MID, the rest FWD.
func slotPosition(p int) fpl.Position {
	switch {
	case p == 1 || p == 12:
		return fpl.GKP
	case p <= 5 || p == 13:
		return fpl.DEF
	case p <= 9 || p == 14:
		return fpl.MID
	}
	return fpl.FWD
}

// world is a small consistent season: a 15-man squad (ids 101-115, three per club
// across clubs 1-5), a market of other players, seven gameweeks of history, and fixtures.
type world struct {
	players  []fpl.Player
	teams    []fpl.Team
	history  []fpl.PlayerHistory
	fixtures fpl.Fixtures
	events   []fpl.Gameweek
}

func newWorld() *world {
	w := &world{}
	for i := 1; i <= 12; i++ {
		w.teams = append(w.teams, fpl.Team{ID: i, Name: fmt.Sprintf("Club%d", i), ShortName: fmt.Sprintf("C%02d", i)})
	}
	// The squad: slot p holds player 100+p.
	for p := 1; p <= 15; p++ {
		w.players = append(w.players, fpl.Player{
			ID: 100 + p, WebName: fmt.Sprintf("Own%02d", p), TeamID: (p-1)%5 + 1, Position: slotPosition(p),
			NowCost: 60, Status: "a", Form: "4.0", TotalPoints: 28,
		})
	}
	// The market: 201-230 spread over positions and clubs 6-12, with distinct strength.
	positions := []fpl.Position{fpl.GKP, fpl.DEF, fpl.DEF, fpl.MID, fpl.MID, fpl.FWD}
	for i := 0; i < 30; i++ {
		w.players = append(w.players, fpl.Player{
			ID: 201 + i, WebName: fmt.Sprintf("Mkt%02d", i), TeamID: 6 + i%7, Position: positions[i%len(positions)],
			NowCost: 45 + (i%8)*8, Status: "a", Form: fmt.Sprintf("%d.0", 2+i%6), TotalPoints: 14 + i,
		})
	}
	// Seven gameweeks of history: everyone plays 90 minutes; points rise with the id so
	// that stronger players are easy to identify.
	for _, p := range w.players {
		for gw := 1; gw <= 7; gw++ {
			pts := 2
			switch {
			case p.ID == 201: // a strong goalkeeper
				pts = 9
			case p.ID == 101: // the squad goalkeeper is poor
				pts = 1
			case p.ID >= 201:
				pts = 2 + (p.ID % 5)
			}
			w.history = append(w.history, fpl.PlayerHistory{PlayerID: p.ID, GameweekID: gw, Fixture: p.ID*10 + gw, TotalPoints: pts, Minutes: 90})
		}
	}
	// Seven finished gameweeks, and GW8 is the one whose deadline is coming.
	for gw := 1; gw <= 7; gw++ {
		w.events = append(w.events, fpl.Gameweek{ID: gw, Finished: true})
	}
	w.events = append(w.events, fpl.Gameweek{ID: 8, IsNext: true}, fpl.Gameweek{ID: 9})
	// GW8: club 2k-1 hosts club 2k. Difficulty 3 everywhere, so fixtures do not decide anything.
	for i := 1; i < 12; i += 2 {
		w.fixtures = append(w.fixtures, fpl.Fixture{ID: i, Event: 8, HomeTeam: i, AwayTeam: i + 1, HomeDifficulty: 3, AwayDifficulty: 3})
	}
	return w
}

func (w *world) player(id int) fpl.Player {
	for _, p := range w.players {
		if p.ID == id {
			return p
		}
	}
	panic(fmt.Sprintf("no player %d", id))
}

// fakeStore serves the world and records the news searches made.
type fakeStore struct {
	w *world

	mu          sync.Mutex
	listCalls   int
	searchCalls []db.SearchFilter
	searchQuery [][]float32
	hits        []db.Hit
	searchErr   error
	newest      time.Time // when the newest stored story was published
	playersAt   time.Time // when the stored players were last written
}

func (s *fakeStore) ListPlayers(context.Context) ([]fpl.Player, error) {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	return append([]fpl.Player(nil), s.w.players...), nil
}
func (s *fakeStore) PlayersUpdatedAt(context.Context) (time.Time, error) { return s.playersAt, nil }
func (s *fakeStore) CountNews(context.Context) (db.NewsCounts, error) {
	return db.NewsCounts{Articles: 12, Chunks: 15, PlayerLinks: 20, Newest: s.newest}, nil
}
func (s *fakeStore) ListTeams(context.Context) ([]fpl.Team, error) { return s.w.teams, nil }
func (s *fakeStore) ListHistory(context.Context) ([]fpl.PlayerHistory, error) {
	return s.w.history, nil
}
func (s *fakeStore) StartingPrices(_ context.Context, ids []int) (map[int]int, error) {
	out := map[int]int{}
	for _, id := range ids {
		out[id] = 60
	}
	return out, nil
}
func (s *fakeStore) FindPlayers(_ context.Context, name string) ([]fpl.Player, error) {
	var out []fpl.Player
	for _, p := range s.w.players {
		if equalFold(p.WebName, name) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *fakeStore) SearchNews(_ context.Context, q []float32, f db.SearchFilter) ([]db.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchCalls = append(s.searchCalls, f)
	s.searchQuery = append(s.searchQuery, q)
	return s.hits, s.searchErr
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 32
		}
		if y >= 'A' && y <= 'Z' {
			y += 32
		}
		if x != y {
			return false
		}
	}
	return true
}

// fakeFPL serves the public API for the test manager and counts calls.
type fakeFPL struct {
	w *world

	mu           sync.Mutex
	entryCalls   int
	fixtureCalls int
	bank         int
	currentEvent int
	captainSlot  int
	viceSlot     int
	entryErr     error
	missing      map[int]bool // team ids FPL answers 404 for

	// What FPL says about the gameweek being scored.
	livePoints     map[int]int             // player id -> points; absent means 0
	liveMinutes    map[int]int             // player id -> minutes; absent means 0
	liveExplain    map[int][]fpl.LiveMatch // player id -> where his points came from
	chipsPlayed    []fpl.ChipUse           // chips the manager has already played
	historyCalls   int
	liveErr        error
	liveCalls      int
	picksCalls     int
	picksErr       error
	autoSubs       []fpl.AutoSub
	chip           *string
	transferCost   int
	extraTransfers []fpl.Transfer
}

func newFakeFPL(w *world) *fakeFPL {
	return &fakeFPL{w: w, bank: 20, currentEvent: 7, captainSlot: 10, viceSlot: 6}
}

func (f *fakeFPL) FetchEntry(_ context.Context, id int) (*fpl.Entry, error) {
	f.mu.Lock()
	f.entryCalls++
	f.mu.Unlock()
	if f.entryErr != nil {
		return nil, f.entryErr
	}
	if f.missing[id] {
		return nil, fmt.Errorf("GET entry: %w", &httpx.StatusError{StatusCode: 404})
	}
	return &fpl.Entry{ID: id, Name: "Test FC", FirstName: "Pat", LastName: "Doe", CurrentEvent: f.currentEvent}, nil
}

func (f *fakeFPL) FetchPicks(context.Context, int, int) (*fpl.Picks, error) {
	f.mu.Lock()
	f.picksCalls++
	f.mu.Unlock()
	if f.picksErr != nil {
		return nil, f.picksErr
	}
	var picks []fpl.Pick
	for p := 1; p <= 15; p++ {
		mult := 1 // the XI scores once, the captain twice, the bench not at all
		switch {
		case p > 11:
			mult = 0
		case p == f.captainSlot:
			mult = 2
		}
		picks = append(picks, fpl.Pick{Element: 100 + p, Position: p, Multiplier: mult, ElementType: slotPosition(p),
			IsCaptain: p == f.captainSlot, IsViceCaptain: p == f.viceSlot})
	}
	return &fpl.Picks{Picks: picks, ActiveChip: f.chip, AutomaticSubs: f.autoSubs,
		EntryHistory: fpl.EntryHistory{Event: f.currentEvent, Points: 55, TotalPoints: 400, OverallRank: 1234567, Bank: f.bank, Value: 1000, EventTransfersCost: f.transferCost}}, nil
}

func (f *fakeFPL) FetchLive(context.Context, int) (*fpl.LiveEvent, error) {
	f.mu.Lock()
	f.liveCalls++
	f.mu.Unlock()
	if f.liveErr != nil {
		return nil, f.liveErr
	}
	var ev fpl.LiveEvent
	for id := 100; id <= 230; id++ {
		var lp fpl.LivePlayer
		lp.ID = id
		lp.Stats.TotalPoints, lp.Stats.Minutes = f.livePoints[id], f.liveMinutes[id]
		lp.Explain = f.liveExplain[id]
		ev.Elements = append(ev.Elements, lp)
	}
	return &ev, nil
}

func (f *fakeFPL) FetchHistory(context.Context, int) (*fpl.History, error) {
	f.mu.Lock()
	f.historyCalls++
	f.mu.Unlock()
	var cur []fpl.GameweekHistory
	for gw := 1; gw <= f.currentEvent; gw++ {
		g := fpl.GameweekHistory{Event: gw}
		if gw == 2 || gw == 3 {
			g.EventTransfers = 1 // used the free one each time: leaves 1 free for the next week
		}
		cur = append(cur, g)
	}
	return &fpl.History{Current: cur, Chips: f.chipsPlayed}, nil
}

func (f *fakeFPL) FetchTransfers(context.Context, int) ([]fpl.Transfer, error) {
	return f.extraTransfers, nil
}

func (f *fakeFPL) FetchBootstrap(context.Context) (*fpl.Bootstrap, error) {
	return &fpl.Bootstrap{Players: f.w.players, Teams: f.w.teams, Gameweeks: f.w.events}, nil
}

func (f *fakeFPL) FetchFixtures(context.Context) (fpl.Fixtures, error) {
	f.mu.Lock()
	f.fixtureCalls++
	f.mu.Unlock()
	return f.w.fixtures, nil
}

// fakeLive serves the logged-in squad, or an error.
type fakeLive struct {
	team  *fpl.MyTeam
	err   error
	calls int
	teams []int // the team id of each call
}

func (f *fakeLive) FetchMyTeam(_ context.Context, id int) (*fpl.MyTeam, error) {
	f.calls++
	f.teams = append(f.teams, id)
	return f.team, f.err
}

// liveTeam is the same 15 players as the public squad, with FPL's own money.
func liveTeam(bank, limit, made, cost int) *fpl.MyTeam {
	t := &fpl.MyTeam{
		Transfers: fpl.MyTransfers{Cost: cost, Limit: limit, Made: made, Bank: bank, Value: 1003},
		Chips:     []fpl.MyChip{{Name: "wildcard", Status: "available"}, {Name: "freehit", Status: "played"}},
	}
	for p := 1; p <= 15; p++ {
		t.Picks = append(t.Picks, fpl.MyPick{Element: 100 + p, Position: p, Multiplier: 1, ElementType: slotPosition(p),
			IsCaptain: p == 10, IsViceCaptain: p == 6, SellingPrice: 59, PurchasePrice: 58})
	}
	return t
}

// fakeEmbedder returns one fixed vector per text.
type fakeEmbedder struct {
	calls [][]string
	kinds []embed.InputType
	err   error
}

func (e *fakeEmbedder) Embed(_ context.Context, texts []string, kind embed.InputType) ([][]float32, error) {
	e.calls = append(e.calls, texts)
	e.kinds = append(e.kinds, kind)
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

// token builds an unsigned bearer token expiring at exp, with a recognisable secret inside.
func token(exp time.Time) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return "Bearer " + enc(`{"alg":"none"}`) + "." + enc(fmt.Sprintf(`{"exp":%d,"sub":"SECRET-SUBJECT"}`, exp.Unix())) + ".SECRET-SIGNATURE"
}

// wrapRejected is the error the live client returns when FPL refuses a session.
func wrapRejected() error { return fmt.Errorf("fetch my team: %w", fpl.ErrSessionRejected) }
