package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func setup(turns ...turn) (*assistant.Session, *scripted, *backend) {
	m := &scripted{turns: turns}
	b := newBackend()
	a := assistant.New(m, b, assistant.Config{Model: "claude-test"})
	return a.NewSession(), m, b
}

func ask(t *testing.T, s *assistant.Session, q string) assistant.Answer {
	t.Helper()
	ans, err := s.Ask(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return ans
}

func TestAsk_AnswersWithoutToolsAndStreams(t *testing.T) {
	s, m, _ := setup(turn{text: []string{"Hello", ", ", "gaffer."}})
	var got []string

	ans, err := s.Ask(context.Background(), "hi", func(p string) { got = append(got, p) })

	if err != nil {
		t.Fatal(err)
	}
	if ans.Text != "Hello, gaffer." || strings.Join(got, "|") != "Hello|, |gaffer." {
		t.Errorf("text %q streamed %v", ans.Text, got)
	}
	if ans.Rounds != 1 || len(ans.ToolCalls) != 0 || len(m.reqs) != 1 {
		t.Errorf("rounds %d tool calls %d requests %d", ans.Rounds, len(ans.ToolCalls), len(m.reqs))
	}
	if len(ans.Warnings()) != 0 {
		t.Errorf("a plain greeting should produce no warnings: %v", ans.Warnings())
	}
}

func TestAsk_TellsTheModelTheRulesItMustFollow(t *testing.T) {
	s, m, _ := setup(turn{text: []string{"ok"}})

	ask(t, s, "Who should I captain?")

	req := m.reqs[0]
	if req.Model != "claude-test" || req.MaxTokens < 500 {
		t.Errorf("model %q max tokens %d", req.Model, req.MaxTokens)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != llm.User || req.Messages[0].Content[0].Text != "Who should I captain?" {
		t.Errorf("messages = %+v", req.Messages)
	}
	// The behaviour the project depends on is written into the prompt, and pinned here.
	for _, rule := range []string{
		"never calculate",       // Go computes; the model narrates
		"exactly as",            // numbers are quoted as the tools give them
		"[",                     // citation syntax
		"untrusted",             // news text is data, not instructions
		"cannot make transfers", // read-only
		"public",                // staleness must be stated
	} {
		if !strings.Contains(strings.ToLower(req.System), strings.ToLower(rule)) {
			t.Errorf("the system prompt lost its rule about %q", rule)
		}
	}
	names := map[string]bool{}
	for _, tool := range req.Tools {
		if names[tool.Name] || tool.Description == "" {
			t.Errorf("tool %q is duplicated or undescribed", tool.Name)
		}
		names[tool.Name] = true
		var schema map[string]any
		if json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
			t.Errorf("tool %q has an invalid input schema: %s", tool.Name, tool.InputSchema)
		}
	}
	for _, want := range []string{"get_my_team", "best_lineup", "suggest_transfers", "find_players", "search_news", "evaluate_transfers"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestAsk_RunsToolsAndReturnsTheirResultsToTheModel(t *testing.T) {
	s, m, b := setup(
		turn{text: []string{"Checking."}, calls: []call{{name: "get_my_team"}}, usage: llm.Usage{InputTokens: 100, OutputTokens: 10}},
		turn{text: []string{"Your captain is Haaland."}, usage: llm.Usage{InputTokens: 150, OutputTokens: 12}},
	)

	ans := ask(t, s, "Who is my captain?")

	if ans.Rounds != 2 || ans.Text != "Checking.Your captain is Haaland." {
		t.Errorf("rounds %d text %q", ans.Rounds, ans.Text)
	}
	if !slices.Equal(b.calls, []string{"get_my_team"}) {
		t.Errorf("backend calls = %v", b.calls)
	}
	// The second request carries the model's own tool_use and our tool_result, ids matching.
	req := m.reqs[1]
	if len(req.Messages) != 3 {
		t.Fatalf("second request has %d messages, want question, assistant tool_use, tool_result", len(req.Messages))
	}
	use := req.Messages[1].Content[len(req.Messages[1].Content)-1]
	res := req.Messages[2].Content[0]
	if use.Type != llm.TypeToolUse || res.Type != llm.TypeToolResult || res.ToolUseID != use.ID || res.IsError {
		t.Errorf("tool_use %+v tool_result %+v", use, res)
	}
	if !strings.Contains(res.Content, "£0.9m") || !strings.Contains(res.Content, "GW7 deadline") {
		t.Errorf("the tool result must carry the view, notes included: %s", res.Content)
	}
	if len(ans.ToolCalls) != 1 || ans.ToolCalls[0].Name != "get_my_team" || ans.ToolCalls[0].Error != "" || ans.ToolCalls[0].Output == "" {
		t.Errorf("tool calls = %+v", ans.ToolCalls)
	}
	if ans.Usage.InputTokens != 250 || ans.Usage.OutputTokens != 22 {
		t.Errorf("usage = %+v, want the two rounds added", ans.Usage)
	}
}

func TestAsk_ParallelToolCallsRunConcurrentlyAndReturnInOrder(t *testing.T) {
	s, m, b := setup(
		turn{calls: []call{{name: "get_my_team"}, {name: "best_lineup"}, {name: "find_players", input: `{"position":"MID"}`}}},
		turn{text: []string{"done"}},
	)
	b.delay = 60 * time.Millisecond

	start := time.Now()
	ask(t, s, "everything")

	if b.maxPar < 2 || time.Since(start) > 170*time.Millisecond {
		t.Errorf("max parallel %d, took %v: independent tools should run together", b.maxPar, time.Since(start))
	}
	results := m.lastToolResults()
	want := []string{"tu_0_0", "tu_0_1", "tu_0_2"}
	for i, r := range results {
		if r.ToolUseID != want[i] {
			t.Errorf("result %d answers %q, want %q: results must follow the order requested", i, r.ToolUseID, want[i])
		}
	}
}

func TestAsk_ToolFailuresGoBackToTheModelAsErrorsAndTheLoopContinues(t *testing.T) {
	tests := []struct {
		name     string
		c        call
		setup    func(b *backend)
		wantText string
	}{
		{"unknown tool", call{name: "make_transfer"}, nil, "unknown tool"},
		{"arguments that are not JSON", call{name: "find_players", input: `{"position":`}, nil, "invalid"},
		{"an argument that does not exist", call{name: "find_players", input: `{"colour":"red"}`}, nil, "colour"},
		{"a wrong argument type", call{name: "find_players", input: `{"limit":"many"}`}, nil, "limit"},
		{"the backend failing", call{name: "get_my_team"}, func(b *backend) { b.teamErr = errors.New("fpl is down") }, "fpl is down"},
		{"the backend panicking", call{name: "best_lineup"}, func(b *backend) { b.panicOn = "best_lineup" }, "panic"},
		{"a news search failing", call{name: "search_news", input: `{"query":"x"}`}, func(b *backend) { b.newsErr = service.ErrNewsUnavailable }, "unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, m, b := setup(turn{calls: []call{tc.c}}, turn{text: []string{"Sorry, that failed."}})
			if tc.setup != nil {
				tc.setup(b)
			}

			ans := ask(t, s, "q")

			res := m.lastToolResults()[0]
			if !res.IsError || !strings.Contains(strings.ToLower(res.Content), strings.ToLower(tc.wantText)) {
				t.Errorf("tool_result = %+v, want an error mentioning %q", res, tc.wantText)
			}
			if ans.Text != "Sorry, that failed." || ans.ToolCalls[0].Error == "" {
				t.Errorf("the model must get to answer after a failed tool: text %q, call %+v", ans.Text, ans.ToolCalls[0])
			}
		})
	}
}

func TestAsk_GivesUpAfterTooManyToolRounds(t *testing.T) {
	loop := turn{calls: []call{{name: "get_my_team"}}}
	m := &scripted{turns: []turn{loop, loop, loop, loop, loop, loop, loop, loop}}
	a := assistant.New(m, newBackend(), assistant.Config{Model: "m", MaxRounds: 3})

	ans, err := a.NewSession().Ask(context.Background(), "q", nil)

	if err == nil || !strings.Contains(err.Error(), "rounds") {
		t.Fatalf("err = %v, want one about too many tool rounds", err)
	}
	if len(m.reqs) != 3 || ans.Rounds != 3 {
		t.Errorf("model called %d times, rounds %d; want exactly 3", len(m.reqs), ans.Rounds)
	}
}

func TestAsk_PassesToolArgumentsToTheBackend(t *testing.T) {
	s, _, b := setup(
		turn{calls: []call{
			{name: "suggest_transfers", input: `{"wildcard":true,"free_transfers":0,"horizon_gameweeks":2,"max_transfers":3}`},
			{name: "find_players", input: `{"position":"MID","team":"ARS","status":"injured","sort_by":"price","max_price":6.5,"min_price":4,"limit":7,"name":"Saka"}`},
			{name: "search_news", input: `{"query":"is Saka injured","player":"Saka","days":3,"limit":4}`},
		}},
		turn{text: []string{"ok"}},
	)

	ask(t, s, "q")

	if tr := b.lastTr; !tr.Wildcard || tr.Free == nil || *tr.Free != 0 || tr.Horizon != 2 || tr.Max != 3 {
		t.Errorf("transfers request = %+v (free: omitted must differ from an explicit 0)", tr)
	}
	want := service.PlayerQuery{Position: "MID", Team: "ARS", Status: "injured", SortBy: "price", MaxPrice: 6.5, MinPrice: 4, Limit: 7, Name: "Saka"}
	if b.lastPl != want {
		t.Errorf("player query = %+v, want %+v", b.lastPl, want)
	}
	if want := (service.NewsQuery{Query: "is Saka injured", Player: "Saka", Days: 3, Limit: 4}); b.lastNews != want {
		t.Errorf("news query = %+v, want %+v", b.lastNews, want)
	}
}

func TestAsk_OmittedFreeTransfersMeansUseTheRealOnes(t *testing.T) {
	s, _, b := setup(turn{calls: []call{{name: "suggest_transfers"}}}, turn{text: []string{"ok"}})

	ask(t, s, "q")

	if b.lastTr.Free != nil {
		t.Errorf("Free = %d, want nil so the service uses the manager's actual free transfers", *b.lastTr.Free)
	}
}

func TestNews_StoriesCarryARefAndNoURLAndAreMarkedUntrusted(t *testing.T) {
	s, m, _ := setup(turn{calls: []call{{name: "search_news", input: `{"query":"saka"}`}}}, turn{text: []string{"ok"}})

	ask(t, s, "q")

	out := m.lastToolResults()[0].Content
	var parsed struct {
		Note    string `json:"note"`
		Stories []struct {
			Ref   int    `json:"ref"`
			Title string `json:"title"`
		} `json:"stories"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(parsed.Stories) != 2 || parsed.Stories[0].Ref != 1 || parsed.Stories[1].Ref != 2 {
		t.Errorf("stories = %+v, want refs 1 and 2", parsed.Stories)
	}
	if !strings.Contains(strings.ToLower(parsed.Note), "untrusted") {
		t.Errorf("note = %q, want the untrusted-content warning next to the stories", parsed.Note)
	}
	// The model never sees a URL, so it cannot invent or alter one. Go builds the source list.
	if strings.Contains(out, "https://") {
		t.Errorf("the tool output exposes URLs to the model: %s", out)
	}
}

func TestCitations(t *testing.T) {
	search := call{name: "search_news", input: `{"query":"saka"}`}
	tests := []struct {
		name        string
		answer      string
		wantSources []int
		wantBad     []int
		wantUncited bool
	}{
		{"a valid citation becomes a source with its URL", "Saka is a doubt [1].", []int{1}, nil, false},
		{"two citations", "Doubt [1] and Haaland is training [2].", []int{1, 2}, nil, false},
		{"a made-up reference is flagged", "Saka is a doubt [1] and out for months [7].", []int{1}, []int{7}, false},
		{"citing nothing after searching news is flagged", "Saka might be a doubt.", nil, nil, true},
		{"the same reference twice is one source", "Doubt [1], confirmed [1].", []int{1}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := setup(turn{calls: []call{search}}, turn{text: []string{tc.answer}})

			ans := ask(t, s, "q")

			var refs []int
			for _, src := range ans.Sources {
				refs = append(refs, src.Ref)
			}
			if !slices.Equal(refs, tc.wantSources) || !slices.Equal(ans.BadCitations, tc.wantBad) || ans.UncitedNews != tc.wantUncited {
				t.Errorf("sources %v bad %v uncited %v; want %v %v %v", refs, ans.BadCitations, ans.UncitedNews, tc.wantSources, tc.wantBad, tc.wantUncited)
			}
		})
	}
}

func TestCitations_SourcesCarryTheRealURLFromGoNotFromTheModel(t *testing.T) {
	s, _, _ := setup(turn{calls: []call{{name: "search_news", input: `{"query":"q"}`}}}, turn{text: []string{"Doubt [1]."}})

	ans := ask(t, s, "q")

	if len(ans.Sources) != 1 || ans.Sources[0].URL != "https://x/saka" || ans.Sources[0].Title != "Saka injury doubt" || ans.Sources[0].Source != "bbc" {
		t.Errorf("sources = %+v", ans.Sources)
	}
}

func TestCitations_NoNewsSearchedMeansNoCitationIsExpected(t *testing.T) {
	s, _, _ := setup(turn{calls: []call{{name: "get_my_team"}}}, turn{text: []string{"Your captain is Haaland."}})

	ans := ask(t, s, "q")

	if ans.UncitedNews || len(ans.BadCitations) != 0 {
		t.Errorf("uncited %v bad %v: an answer from structured tools needs no citation", ans.UncitedNews, ans.BadCitations)
	}
}

func TestCitations_ARepeatedStoryKeepsItsRefAndRefsContinueAcrossTurns(t *testing.T) {
	m := &scripted{turns: []turn{
		{calls: []call{{name: "search_news", input: `{"query":"a"}`}}}, {text: []string{"First [1]."}},
		{calls: []call{{name: "search_news", input: `{"query":"b"}`}}}, {text: []string{"Second [1][3]."}},
	}}
	b := newBackend()
	s := assistant.New(m, b, assistant.Config{Model: "m"}).NewSession()
	ask(t, s, "first question")
	// The second search returns one story already seen (Saka) and one new one.
	b.news = []service.NewsView{b.news[0], {Title: "New story", Content: "c", Source: "bbc", URL: "https://x/new", Age: "1h"}}

	ans := ask(t, s, "second question")

	out := m.lastToolResults()[0].Content
	if !strings.Contains(out, `"ref":1`) || !strings.Contains(out, `"ref":3`) || strings.Contains(out, `"ref":4`) {
		t.Errorf("refs = %s; the seen story keeps ref 1 and the new one gets 3", out)
	}
	if len(ans.BadCitations) != 0 || len(ans.Sources) != 2 {
		t.Errorf("sources %+v bad %v", ans.Sources, ans.BadCitations)
	}
	if len(m.reqs[3].Messages) != 7 { // q1, tool_use, tool_result, a1, q2, tool_use, tool_result
		t.Errorf("the second turn's final request has %d messages, want the whole conversation (7)", len(m.reqs[3].Messages))
	}
}

func TestGrounding(t *testing.T) {
	tests := []struct {
		name     string
		question string
		answer   string
		want     []string
	}{
		{"numbers straight from the tool", "q", "Haaland is projected at 6.7 xP and your bank is £0.9m.", nil},
		{"a number the model computed itself", "q", "Your bank is £2.3m.", []string{"2.3"}},
		{"a sum the model did", "q", "Haaland and your bank together are £22.0m.", []string{"22.0"}},
		{"a trailing zero is the same number", "q", "Haaland projects at 6.70 xP.", nil},
		{"numbers the user mentioned are fine", "Should I spend £4.5m on a forward?", "Spending £4.5m is possible with your £0.9m bank.", nil},
		{"citation markers are not numbers", "q", "Haaland projects at 6.7 [1] [2].", nil},
		{"list numbering is not a number", "q", "1. Haaland at 6.7 xP\n2. Rice at 5.6 xP", nil},
		{"gameweek labels match the tool", "q", "In GW8 Haaland projects at 6.7 xP.", nil},
		{"two unsupported numbers are both reported", "q", "Haaland scores 11.4 and Rice 9.9.", []string{"11.4", "9.9"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, b := setup(turn{calls: []call{{name: "get_my_team"}, {name: "find_players"}}}, turn{text: []string{tc.answer}})
			b.players = []service.PlayerView{{Name: "Rice", Price: "£7.3m", XP: 5.6}}

			ans := ask(t, s, tc.question)

			if !slices.Equal(ans.Ungrounded, tc.want) {
				t.Errorf("ungrounded = %v, want %v", ans.Ungrounded, tc.want)
			}
		})
	}
}

func TestWarningsDescribeEveryProblemInPlainWords(t *testing.T) {
	s, _, _ := setup(turn{calls: []call{{name: "search_news", input: `{"query":"q"}`}}}, turn{text: []string{"Doubt [9]. It is worth 99.5 points."}})

	w := strings.Join(ask(t, s, "q").Warnings(), "\n")

	for _, want := range []string{"[9]", "99.5"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings %q should mention %q", w, want)
		}
	}
}

func TestAsk_ToolOutputIsBoundedAndNewsIsTrimmed(t *testing.T) {
	s, m, b := setup(turn{calls: []call{{name: "find_players"}, {name: "search_news", input: `{"query":"q"}`}}}, turn{text: []string{"ok"}})
	for i := 0; i < 400; i++ {
		b.players = append(b.players, service.PlayerView{Name: fmt.Sprintf("Player%03d", i), Team: "ARS", Position: "MID", Price: "£5.0m", Status: "available"})
	}
	b.news[0].Content = strings.Repeat("word ", 2000)

	ask(t, s, "q")

	results := m.lastToolResults()
	if len(results[0].Content) > 26_000 || !strings.Contains(results[0].Content, "truncated") {
		t.Errorf("a huge result should be capped and say so: %d bytes", len(results[0].Content))
	}
	if len(results[1].Content) > 3_000 {
		t.Errorf("a long article should be trimmed before it reaches the model: %d bytes", len(results[1].Content))
	}
}

func TestAsk_RejectsAnEmptyQuestionWithoutCallingTheModel(t *testing.T) {
	s, m, _ := setup()

	if _, err := s.Ask(context.Background(), "   ", nil); err == nil {
		t.Error("want an error")
	}
	if len(m.reqs) != 0 {
		t.Error("the model was called for an empty question")
	}
}

func TestAsk_ModelErrorsSurfaceWithWhatArrived(t *testing.T) {
	boom := &llm.APIError{Type: "overloaded_error", Message: "Overloaded"}
	s, _, _ := setup(turn{text: []string{"partial "}, err: boom})

	ans, err := s.Ask(context.Background(), "q", nil)

	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Type != "overloaded_error" {
		t.Fatalf("err = %v", err)
	}
	if ans.Text != "partial " {
		t.Errorf("partial text = %q", ans.Text)
	}
}

func TestAsk_StopsWhenTheContextIsCancelled(t *testing.T) {
	s, _, b := setup(turn{calls: []call{{name: "get_my_team"}}}, turn{text: []string{"never"}})
	b.delay = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Ask(ctx, "q", nil)

	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Error("took too long to stop")
	}
}

func TestAsk_AFailedTurnIsNotCommittedToTheConversation(t *testing.T) {
	// The first turn dies after a tool call. If its half-finished messages stayed in the
	// history, the next request would contain a tool_use with no tool_result, which the API rejects.
	m := &scripted{turns: []turn{
		{calls: []call{{name: "get_my_team"}}},
		{err: &llm.APIError{Type: "overloaded_error", Message: "Overloaded"}},
		{text: []string{"Back to normal."}},
	}}
	s := assistant.New(m, newBackend(), assistant.Config{Model: "m"}).NewSession()

	if _, err := s.Ask(context.Background(), "first", nil); err == nil {
		t.Fatal("want the first turn to fail")
	}
	ans, err := s.Ask(context.Background(), "second", nil)

	if err != nil || ans.Text != "Back to normal." {
		t.Fatalf("err = %v text = %q", err, ans.Text)
	}
	retry := m.reqs[2]
	if len(retry.Messages) != 1 || retry.Messages[0].Content[0].Text != "second" {
		t.Errorf("the retry request carries %d messages (%+v); the failed turn must leave no trace", len(retry.Messages), retry.Messages)
	}
}

func TestAsk_ReportsWhenTheAnswerWasCutOffAtTheTokenLimit(t *testing.T) {
	m := &truncating{}
	s := assistant.New(m, newBackend(), assistant.Config{Model: "m"}).NewSession()

	ans, err := s.Ask(context.Background(), "q", nil)

	if err != nil || !ans.Truncated {
		t.Fatalf("err %v truncated %v", err, ans.Truncated)
	}
	if !strings.Contains(strings.Join(ans.Warnings(), " "), "cut off") {
		t.Errorf("warnings = %v", ans.Warnings())
	}
}

// truncating is a Model whose reply stops at the token limit.
type truncating struct{}

func (truncating) Stream(_ context.Context, _ llm.Request, onText func(string)) (llm.Response, error) {
	onText("A long answer that")
	return llm.Response{Content: []llm.Block{llm.Text("A long answer that")}, StopReason: "max_tokens"}, nil
}

// whoBackend records whose squad each tool call was made for.
type whoBackend struct {
	*backend
	mu   sync.Mutex
	seen []service.Viewer
}

func (b *whoBackend) MyTeam(ctx context.Context) (service.MyTeamView, error) {
	b.mu.Lock()
	b.seen = append(b.seen, service.ViewerFrom(ctx))
	b.mu.Unlock()
	return b.backend.MyTeam(ctx)
}

// The web client says whose squad a question is about in the request; the model's tool calls
// must carry that through, or it would answer about the wrong manager.
func TestAsk_ToolCallsSeeTheViewerOfTheRequest(t *testing.T) {
	m := &scripted{turns: []turn{
		{calls: []call{{name: "get_my_team"}}},
		{text: []string{"done"}},
	}}
	b := &whoBackend{backend: newBackend()}
	s := assistant.New(m, b, assistant.Config{Model: "claude-test"}).NewSession()
	want := service.Viewer{TeamID: 4242, Token: "Bearer abc"}

	if _, err := s.Ask(service.WithViewer(context.Background(), want), "my team?", nil); err != nil {
		t.Fatal(err)
	}

	if len(b.seen) != 1 || b.seen[0] != want {
		t.Errorf("the tool saw %+v, want %+v", b.seen, want)
	}
}

func whatIfView() service.WhatIfView {
	pv := func(name string) service.PlayerView {
		return service.PlayerView{ID: 1, Name: name, Team: "BHA", Position: "MID", Price: "£5.0m"}
	}
	return service.WhatIfView{
		Gameweek: 9, Horizon: 4, Valid: true, Problems: []string{}, Transfers: 1, FreeTransfers: 1, FreeNextWeek: 1,
		BankBefore: "£0.9m", BankAfter: "£0.2m", GainPerGameweek: 1.1, NetPoints: 4.4, Verdict: "worth_it",
		Insights: []string{"Worth doing: it adds +4.4 points over 4 gameweeks after the cost of the transfers."},
		Moves:    []service.WhatIfMoveView{{Out: pv("Hughes"), In: pv("Gomez")}},
		Weeks:    []service.WhatIfWeekView{{Gameweek: 9, Before: 60, After: 61.1, Delta: 1.1}},
		Before:   service.WhatIfLineupView{Formation: "4-3-3", ExpectedPoints: 60, Captain: "Haaland", Starting: []service.PlayerView{pv("LONGLIST-A")}, Bench: []service.PlayerView{pv("LONGLIST-B")}},
		After:    service.WhatIfLineupView{Formation: "4-3-3", ExpectedPoints: 61.1, Captain: "Haaland", Starting: []service.PlayerView{pv("LONGLIST-C")}, Bench: []service.PlayerView{pv("LONGLIST-D")}},
		Notes:    []string{},
	}
}

func TestAsk_EvaluateTransfersPassesTheNamesAsTheManagerSaidThemAndReturnsTheVerdict(t *testing.T) {
	s, m, b := setup(
		turn{calls: []call{{name: "evaluate_transfers", input: `{"moves":[{"sell":"Hughes","buy":"Gomez","buy_club":"BHA"}],"horizon_gameweeks":6,"free_transfers":2}`}}},
		turn{text: []string{"Worth doing."}},
	)
	b.whatIf = whatIfView()

	ask(t, s, "What if I swap Hughes for Gomez?")

	zero2 := 2
	got := b.lastWhatIf
	if len(got.Moves) != 1 || got.Moves[0] != (service.NamedMove{Out: "Hughes", In: "Gomez", InTeam: "BHA"}) || got.Horizon != 6 || got.Free == nil || *got.Free != zero2 {
		t.Errorf("the backend was asked %+v", got)
	}
	res := m.lastToolResults()[0]
	if res.IsError || !strings.Contains(res.Content, `"verdict":"worth_it"`) || !strings.Contains(res.Content, "Worth doing") || !strings.Contains(res.Content, `"net_points_over_horizon":4.4`) {
		t.Errorf("the model must get the verdict and figures: %s", res.Content)
	}
	if strings.Contains(res.Content, "LONGLIST") {
		t.Errorf("the full lineups are long and the insights already say who starts: they must be left out: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"captain":"Haaland"`) {
		t.Errorf("the lineup summary must stay: %s", res.Content)
	}
}

func TestAsk_EvaluateTransfersIsStrictAboutItsArguments(t *testing.T) {
	for name, in := range map[string]string{
		"no moves":      `{"moves":[]}`,
		"missing moves": `{}`,
		"unknown field": `{"moves":[{"sell":"A","buy":"B","force":true}]}`,
		"wrong type":    `{"moves":[{"sell":1,"buy":"B"}]}`,
		"ids not names": `{"moves":[{"out":101,"in":201}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, m, b := setup(turn{calls: []call{{name: "evaluate_transfers", input: in}}}, turn{text: []string{"ok"}})

			ask(t, s, "q")

			res := m.lastToolResults()[0]
			if !res.IsError {
				t.Errorf("want an error the model can read and correct, got %s", res.Content)
			}
			if len(b.lastWhatIf.Moves) != 0 {
				t.Errorf("a malformed call must not reach the backend: %+v", b.lastWhatIf)
			}
		})
	}
}

func TestAsk_EvaluateTransfersShowsTheModelWhyANameFailed(t *testing.T) {
	s, m, b := setup(
		turn{calls: []call{{name: "evaluate_transfers", input: `{"moves":[{"sell":"Messi","buy":"Gomez"}]}`}}},
		turn{text: []string{"He is not in your squad."}},
	)
	b.whatIfErr = &service.InputError{Msg: `none of your players is called "Messi". Your squad: Raya, Haaland`}

	ans := ask(t, s, "What if I sell Messi?")

	res := m.lastToolResults()[0]
	if !res.IsError || !strings.Contains(res.Content, "Your squad: Raya, Haaland") {
		t.Errorf("the model needs the helpful message to answer: %+v", res)
	}
	if len(ans.ToolCalls) != 1 || ans.ToolCalls[0].Error == "" {
		t.Errorf("tool calls = %+v", ans.ToolCalls)
	}
}

func TestAsk_EvaluateTransfersReportsAnIllegalPlanAsDataNotAsAnError(t *testing.T) {
	s, m, b := setup(
		turn{calls: []call{{name: "evaluate_transfers", input: `{"moves":[{"sell":"Raya","buy":"Haaland"}]}`}}},
		turn{text: []string{"That breaks a rule."}},
	)
	b.whatIf = service.WhatIfView{Valid: false, Problems: []string{"Raya (GKP) cannot be replaced by Haaland (FWD): a transfer must be like for like."}, Insights: []string{}}

	ask(t, s, "What if I swap Raya for Haaland?")

	res := m.lastToolResults()[0]
	if res.IsError || !strings.Contains(res.Content, `"valid":false`) || !strings.Contains(res.Content, "like for like") {
		t.Errorf("a rule break is a normal answer: %+v", res)
	}
}

var testClubs = map[string]string{"MCI": "Man City", "BHA": "Brighton", "BOU": "Bournemouth", "NEW": "Newcastle", "TOT": "Spurs", "WOL": "Wolves"}

func setupClubs(turns ...turn) (*assistant.Session, *scripted, *backend) {
	m := &scripted{turns: turns}
	b := newBackend()
	a := assistant.New(m, b, assistant.Config{Model: "claude-test", Clubs: testClubs})
	return a.NewSession(), m, b
}

func TestClubs_TheModelIsToldThisSeasonsClubsAndNotToUseMemory(t *testing.T) {
	s, m, _ := setupClubs(turn{text: []string{"hi"}})

	ask(t, s, "hello")

	sys := m.reqs[0].System
	for _, want := range []string{"Clubs", "BHA = Brighton", "BOU = Bournemouth", "MCI = Man City", "never from memory"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	if strings.Index(sys, "BHA = Brighton") < strings.Index(sys, "Style") {
		t.Errorf("the club list belongs at the end, after the standing rules")
	}
}

func TestClubs_ANameFromMemoryThatTheDataNeverMentionedIsFlagged(t *testing.T) {
	// The squad view (a Haaland fixture against BHA) is what the tool returned; the answer says Bournemouth.
	b := newBackend()
	b.team.Starting[0].Fixture = "BHA (H)"
	m := &scripted{turns: []turn{{calls: []call{{name: "get_my_team"}}}, {text: []string{"Haaland plays Bournemouth at home."}}}}
	s := assistant.New(m, b, assistant.Config{Model: "claude-test", Clubs: testClubs}).NewSession()

	ans := ask(t, s, "Who does Haaland play?")

	if !slices.Equal(ans.UnknownClubs, []string{"Bournemouth"}) {
		t.Errorf("unknown clubs = %v", ans.UnknownClubs)
	}
	if w := strings.Join(ans.Warnings(), " "); !strings.Contains(w, "Bournemouth") {
		t.Errorf("the warning must name it: %q", w)
	}
}

func TestClubs_ARightNameIsNotFlagged(t *testing.T) {
	cases := map[string]string{
		"by name, from the data":      "Haaland plays Brighton at home.",
		"by code":                     "Haaland plays BHA at home.",
		"an alias of a known club":    "Haaland is at Brighton and Hove Albion.",
		"the manager's own club word": "Your Man City striker plays Brighton.",
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBackend()
			b.team.Starting[0].Fixture = "BHA (H)"
			b.team.Starting[0].Team = "MCI"
			m := &scripted{turns: []turn{{calls: []call{{name: "get_my_team"}}}, {text: []string{answer}}}}
			s := assistant.New(m, b, assistant.Config{Model: "claude-test", Clubs: testClubs}).NewSession()

			ans := ask(t, s, "Who does Haaland play?")

			if len(ans.UnknownClubs) != 0 {
				t.Errorf("%q flagged %v", answer, ans.UnknownClubs)
			}
		})
	}
}

func TestClubs_AClubTheManagerNamedIsKnownEvenIfNoToolReturnedIt(t *testing.T) {
	s, _, _ := setupClubs(turn{text: []string{"I do not have Spurs fixtures to hand."}})

	ans := ask(t, s, "How are Spurs doing?")

	if len(ans.UnknownClubs) != 0 {
		t.Errorf("the manager said Spurs: %v", ans.UnknownClubs)
	}
}

func TestClubs_AClubNamedInNewsTextIsKnown(t *testing.T) {
	m := &scripted{turns: []turn{{calls: []call{{name: "search_news", input: `{"query":"x"}`}}}, {text: []string{"Wolves are mentioned."}}}}
	b := newBackend()
	b.news[0].Content = "Wolves host a depleted side."
	s := assistant.New(m, b, assistant.Config{Model: "claude-test", Clubs: testClubs}).NewSession()

	ans := ask(t, s, "news?")

	if len(ans.UnknownClubs) != 0 {
		t.Errorf("%v", ans.UnknownClubs)
	}
}

func TestClubs_NoLegendMeansNoCheck(t *testing.T) {
	s, m, _ := setup(turn{text: []string{"Bournemouth, Brighton, anything."}})

	ans := ask(t, s, "q")

	if len(ans.UnknownClubs) != 0 || strings.Contains(m.reqs[0].System, "Clubs\n") {
		t.Errorf("without a legend: %v", ans.UnknownClubs)
	}
}

func TestClubs_WhatIsKnownCarriesOverToTheNextQuestion(t *testing.T) {
	b := newBackend()
	b.team.Starting[0].Fixture = "BHA (H)"
	m := &scripted{turns: []turn{{calls: []call{{name: "get_my_team"}}}, {text: []string{"BHA at home."}}, {text: []string{"Brighton again."}}}}
	s := assistant.New(m, b, assistant.Config{Model: "claude-test", Clubs: testClubs}).NewSession()

	ask(t, s, "q1")
	ans := ask(t, s, "and who is that?")

	if len(ans.UnknownClubs) != 0 {
		t.Errorf("Brighton was in an earlier result of this conversation: %v", ans.UnknownClubs)
	}
}

func TestAsk_NumbersInAToolsErrorMessageCountAsGrounded(t *testing.T) {
	s, _, b := setup(
		turn{calls: []call{{name: "evaluate_transfers", input: `{"moves":[{"sell":"Messi","buy":"Gomez"}]}`}}},
		turn{text: []string{"Messi is not one of your 15 players."}},
	)
	b.whatIfErr = &service.InputError{Msg: `none of your players is called "Messi". Your squad (15 players): Raya, Haaland`}

	ans := ask(t, s, "What if I sell Messi?")

	if len(ans.Ungrounded) != 0 {
		t.Errorf("the 15 came from the tool's own message: %v", ans.Ungrounded)
	}
}
