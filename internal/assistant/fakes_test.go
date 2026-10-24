package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// call is a tool call the scripted model makes.
type call struct {
	name  string
	input string // JSON; empty means {}
}

// turn is one scripted reply from the model.
type turn struct {
	text  []string // streamed pieces
	calls []call
	usage llm.Usage
	err   error
}

// scripted is a Model that plays back turns and records what it was sent.
type scripted struct {
	mu    sync.Mutex
	turns []turn
	reqs  []llm.Request
	n     int
}

func (m *scripted) Stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	i := len(m.reqs) - 1
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	if i >= len(m.turns) {
		return llm.Response{}, errors.New("scripted model ran out of turns")
	}
	t := m.turns[i]
	var resp llm.Response
	text := ""
	for _, p := range t.text {
		text += p
		if onText != nil {
			onText(p)
		}
	}
	if text != "" {
		resp.Content = append(resp.Content, llm.Text(text))
	}
	for j, c := range t.calls {
		in := c.input
		if in == "" {
			in = "{}"
		}
		resp.Content = append(resp.Content, llm.ToolUse(fmt.Sprintf("tu_%d_%d", i, j), c.name, json.RawMessage(in)))
	}
	resp.StopReason = "end_turn"
	if len(t.calls) > 0 {
		resp.StopReason = "tool_use"
	}
	resp.Usage = t.usage
	return resp, t.err
}

// lastToolResults returns the tool_result blocks in the final request's last message.
func (m *scripted) lastToolResults() []llm.Block {
	req := m.reqs[len(m.reqs)-1]
	last := req.Messages[len(req.Messages)-1]
	var out []llm.Block
	for _, b := range last.Content {
		if b.Type == llm.TypeToolResult {
			out = append(out, b)
		}
	}
	return out
}

// backend is a Backend returning canned views and recording calls.
type backend struct {
	mu sync.Mutex

	team     service.MyTeamView
	lineup   service.LineupView
	transfer service.TransfersView
	players  []service.PlayerView
	news     []service.NewsView

	teamErr    error
	newsErr    error
	panicOn    string
	delay      time.Duration
	calls      []string
	whatIf     service.WhatIfView
	whatIfErr  error
	lastWhatIf service.WhatIfNamedRequest
	lastNews   service.NewsQuery
	lastTr     service.TransfersRequest
	lastPl     service.PlayerQuery
	running    int
	maxPar     int
}

func (b *backend) enter(name string) func() {
	b.mu.Lock()
	b.calls = append(b.calls, name)
	b.running++
	b.maxPar = max(b.maxPar, b.running)
	b.mu.Unlock()
	if b.panicOn == name {
		panic("boom")
	}
	time.Sleep(b.delay)
	return func() { b.mu.Lock(); b.running--; b.mu.Unlock() }
}

func (b *backend) MyTeam(context.Context) (service.MyTeamView, error) {
	defer b.enter("get_my_team")()
	return b.team, b.teamErr
}
func (b *backend) Lineup(context.Context) (service.LineupView, error) {
	defer b.enter("best_lineup")()
	return b.lineup, nil
}
func (b *backend) Transfers(_ context.Context, r service.TransfersRequest) (service.TransfersView, error) {
	defer b.enter("suggest_transfers")()
	b.lastTr = r
	return b.transfer, nil
}
func (b *backend) FindPlayers(_ context.Context, q service.PlayerQuery) ([]service.PlayerView, error) {
	defer b.enter("find_players")()
	b.lastPl = q
	return b.players, nil
}
func (b *backend) WhatIfNamed(_ context.Context, r service.WhatIfNamedRequest) (service.WhatIfView, error) {
	defer b.enter("evaluate_transfers")()
	b.lastWhatIf = r
	return b.whatIf, b.whatIfErr
}
func (b *backend) SearchNews(_ context.Context, q service.NewsQuery) ([]service.NewsView, error) {
	defer b.enter("search_news")()
	b.lastNews = q
	return b.news, b.newsErr
}

func newBackend() *backend {
	return &backend{
		team: service.MyTeamView{TeamName: "Test FC", Gameweek: 8, Source: "public", Bank: "£0.9m", FreeTransfers: 1,
			Captain: "Haaland", Notes: []string{"This squad is as of the GW7 deadline."},
			Starting: []service.PlayerView{{Name: "Haaland", Price: "£15.7m", XP: 6.7, Position: "FWD"}}},
		lineup: service.LineupView{Gameweek: 8, Formation: "4-3-3", ExpectedPoints: 64.9, Captain: "Haaland", ViceCaptain: "Joao Pedro", CaptainLead: 0.04},
		transfer: service.TransfersView{Gameweek: 8, FreeTransfers: 1, Bank: "£0.9m", Horizon: 4,
			Suggestions: []service.SuggestionView{{Transfers: 1, Hit: 0, NetPoints: 7.1, GainPerGameweek: 1.8, BankAfter: "£0.3m"}}},
		players: []service.PlayerView{{Name: "Rice", Team: "ARS", Position: "MID", Price: "£7.3m", XP: 5.6}},
		news: []service.NewsView{
			{Title: "Saka injury doubt", Content: "Arteta says Saka is a doubt.", Source: "bbc", URL: "https://x/saka", Age: "2h", Published: "2026-10-22T01:00:00Z", Players: []string{"Saka"}},
			{Title: "Haaland trains", Content: "Haaland back in training.", Source: "sky", URL: "https://x/haaland", Age: "5h", Published: "2026-10-21T22:00:00Z", Players: []string{"Haaland"}},
		},
	}
}
