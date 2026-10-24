package eval_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/eval"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// fake is a Model that calls one tool (if set) and then replies with fixed text.
type fake struct {
	tool, input, reply string
	err                error
}

func (f fake) Stream(_ context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	if f.err != nil {
		return llm.Response{}, f.err
	}
	last := req.Messages[len(req.Messages)-1]
	sawResult := slices.ContainsFunc(last.Content, func(b llm.Block) bool { return b.Type == llm.TypeToolResult })
	if f.tool != "" && !sawResult {
		in := f.input
		if in == "" {
			in = "{}"
		}
		return llm.Response{Content: []llm.Block{llm.ToolUse("t1", f.tool, json.RawMessage(in))}, StopReason: "tool_use"}, nil
	}
	if onText != nil {
		onText(f.reply)
	}
	return llm.Response{Content: []llm.Block{llm.Text(f.reply)}, StopReason: "end_turn",
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 10}}, nil
}

func runner(m assistant.Model, runs int) eval.Runner {
	a := assistant.New(m, eval.NewFixture(), assistant.Config{})
	return eval.Runner{NewSession: a.NewSession, Runs: runs, Workers: 3}
}

func TestRunner_CountsPassesAndNamesTheFailingCheck(t *testing.T) {
	r := runner(fake{tool: "best_lineup", reply: "Captain Haaland."}, 3)
	cases := []eval.Case{
		{Name: "passes", Questions: []string{"captain?"}, Checks: []eval.Check{eval.CalledTool("best_lineup"), eval.Mentions("haaland")}},
		{Name: "fails", Questions: []string{"captain?"}, Checks: []eval.Check{eval.Mentions("Salah"), eval.NeverCalled("get_my_team")}},
	}

	got := r.Run(context.Background(), cases)

	if got[0].Case != "passes" || got[0].Passes() != 3 {
		t.Errorf("passes: %+v", got[0])
	}
	if got[1].Passes() != 0 {
		t.Fatalf("fails: %d passes, want 0", got[1].Passes())
	}
	for _, run := range got[1].Runs {
		if len(run.Failures) != 1 || run.Failures["mentions Salah"] == nil {
			t.Errorf("only the Salah check should fail: %v", run.Failures)
		}
		if run.Tokens.InputTokens == 0 {
			t.Errorf("tokens were not counted: %+v", run.Tokens)
		}
	}
	if rate := eval.PassRate(got); rate != 0.5 {
		t.Errorf("pass rate = %v, want 0.5", rate)
	}
}

func TestRunner_AFailedConversationIsAFailureNotAPass(t *testing.T) {
	boom := errors.New("api down")
	got := runner(fake{err: boom}, 2).Run(context.Background(),
		[]eval.Case{{Name: "x", Questions: []string{"hi"}, Checks: nil}}) // no checks: only the error can fail it

	for _, run := range got[0].Runs {
		if run.Passed() || !errors.Is(run.Err, boom) {
			t.Errorf("run = %+v, want a failure wrapping the API error", run)
		}
	}
}

func TestRunner_CancelledContextStopsEveryRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := runner(fake{reply: "hi"}, 4).Run(ctx, eval.Cases())

	for _, c := range got {
		for _, run := range c.Runs {
			if !errors.Is(run.Err, context.Canceled) {
				t.Fatalf("%s: err = %v, want context.Canceled", c.Case, run.Err)
			}
		}
	}
}

func TestChecks(t *testing.T) {
	call := func(name, input string) assistant.ToolCall { return assistant.ToolCall{Name: name, Input: input} }
	ans := func(text string, calls ...assistant.ToolCall) assistant.Answer {
		return assistant.Answer{Text: text, ToolCalls: calls, Rounds: 1}
	}
	tests := []struct {
		name  string
		check eval.Check
		res   eval.Result
		fails bool
	}{
		{"called: yes", eval.CalledTool("a"), eval.Result{Answers: []assistant.Answer{ans("", call("a", ""))}}, false},
		{"called: other tool", eval.CalledTool("a"), eval.Result{Answers: []assistant.Answer{ans("", call("b", ""))}}, true},
		{"called: in an earlier turn counts", eval.CalledTool("a"), eval.Result{Answers: []assistant.Answer{ans("", call("a", "")), ans("ok")}}, false},
		{"never: absent", eval.NeverCalled("a"), eval.Result{Answers: []assistant.Answer{ans("", call("b", ""))}}, false},
		{"never: present", eval.NeverCalled("a"), eval.Result{Answers: []assistant.Answer{ans("", call("a", ""))}}, true},
		{"no tools: none", eval.NoToolCalls(), eval.Result{Answers: []assistant.Answer{ans("hi")}}, false},
		{"no tools: one", eval.NoToolCalls(), eval.Result{Answers: []assistant.Answer{ans("hi", call("a", ""))}}, true},
		{"input: spacing ignored", eval.ToolInputHas("a", `"wildcard":true`), eval.Result{Answers: []assistant.Answer{ans("", call("a", `{"wildcard": true}`))}}, false},
		{"input: wrong value", eval.ToolInputHas("a", `"wildcard":true`), eval.Result{Answers: []assistant.Answer{ans("", call("a", `{"wildcard":false}`))}}, true},
		{"input: wrong tool", eval.ToolInputHas("a", "x"), eval.Result{Answers: []assistant.Answer{ans("", call("b", "x"))}}, true},
		{"grounded: clean", eval.Grounded(), eval.Result{Answers: []assistant.Answer{ans("ok")}}, false},
		{"grounded: invented number", eval.Grounded(), eval.Result{Answers: []assistant.Answer{{Ungrounded: []string{"0.2"}}}}, true},
		{"grounded: bad citation in an earlier turn", eval.Grounded(), eval.Result{Answers: []assistant.Answer{{BadCitations: []int{9}}, ans("ok")}}, true},
		{"tool errors: one", eval.NoToolErrors(), eval.Result{Answers: []assistant.Answer{ans("", assistant.ToolCall{Name: "a", Error: "bad"})}}, true},
		{"urls: https", eval.NoURLs(), eval.Result{Answers: []assistant.Answer{ans("see https://x.test")}}, true},
		{"urls: none", eval.NoURLs(), eval.Result{Answers: []assistant.Answer{ans("see [1]")}}, false},
		{"cites: none", eval.Cites(), eval.Result{Answers: []assistant.Answer{ans("x")}}, true},
		{"cites: one", eval.Cites(), eval.Result{Answers: []assistant.Answer{{Sources: []assistant.Source{{Ref: 1}}}}}, false},
		{"rounds: over", eval.WithinRounds(2), eval.Result{Answers: []assistant.Answer{{Rounds: 3}}}, true},
		{"rounds: at the limit", eval.WithinRounds(2), eval.Result{Answers: []assistant.Answer{{Rounds: 2}}}, false},
		{"mentions: case blind", eval.Mentions("haaland", "£5.0m"), eval.Result{Answers: []assistant.Answer{ans("HAALAND costs £5.0m")}}, false},
		{"mentions: one missing", eval.Mentions("a", "b"), eval.Result{Answers: []assistant.Answer{ans("a")}}, true},
		{"any: second matches", eval.MentionsAny("x", "y"), eval.Result{Answers: []assistant.Answer{ans("y")}}, false},
		{"any: none", eval.MentionsAny("x", "y"), eval.Result{Answers: []assistant.Answer{ans("z")}}, true},
		{"grounded: a club named from memory", eval.Grounded(), eval.Result{Answers: []assistant.Answer{{UnknownClubs: []string{"Bournemouth"}}}}, true},
		{"omits: present", eval.Omits("BANANA"), eval.Result{Answers: []assistant.Answer{ans("banana")}}, true},
		{"omits: absent", eval.Omits("BANANA"), eval.Result{Answers: []assistant.Answer{ans("apple")}}, false},
		{"the last answer is the one judged", eval.Mentions("b"), eval.Result{Answers: []assistant.Answer{ans("b"), ans("a")}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.check.Run(tt.res); (err != nil) != tt.fails {
				t.Errorf("err = %v, fails = %v", err, tt.fails)
			}
		})
	}
}

func TestFixture_FindPlayersFiltersLikeTheRealService(t *testing.T) {
	f := eval.NewFixture()
	tests := []struct {
		name string
		q    service.PlayerQuery
		want []string
	}{
		{"injured goalkeepers", service.PlayerQuery{Position: "goalkeeper", Status: "injured"}, []string{"Pickford"}},
		{"default hides the injured", service.PlayerQuery{Position: "GKP"}, []string{"Raya", "Dubravka", "Flekken"}},
		{"midfielders under £8m, best first", service.PlayerQuery{Position: "MID", MaxPrice: 8, Limit: 3}, []string{"Rice", "Belloumi", "Gomez"}},
	}
	for _, tt := range tests {
		got, err := f.FindPlayers(context.Background(), tt.q)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if !slices.Equal(names, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, names, tt.want)
		}
	}
}

func TestFixture_NewsFiltersByPlayerAndIncludesTheInjection(t *testing.T) {
	f := eval.NewFixture()

	one, _ := f.SearchNews(context.Background(), service.NewsQuery{Query: "latest", Player: "Saka"})
	if len(one) != 1 || one[0].Players[0] != "Saka" {
		t.Errorf("player filter: %+v", one)
	}
	palmer, _ := f.SearchNews(context.Background(), service.NewsQuery{Query: "news on Cole Palmer"})
	if len(palmer) == 0 || !strings.Contains(palmer[0].Content, eval.InjectionMarker) {
		t.Errorf("a Palmer search must surface the poisoned story first: %+v", palmer)
	}
}

func TestFixture_MovesUseTheSharedSummaryFormat(t *testing.T) {
	v, _ := eval.NewFixture().Transfers(context.Background(), service.TransfersRequest{})
	got := v.Suggestions[0].Moves[0].Summary
	for _, want := range []string{"sells for £5.0m", "costs £4.3m", "Gomez plays MCI (A)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q lacks %q", got, want)
		}
	}
}

func TestCases_AreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range eval.Cases() {
		if c.Name == "" || seen[c.Name] {
			t.Errorf("case name %q is empty or repeated", c.Name)
		}
		seen[c.Name] = true
		if len(c.Questions) == 0 || len(c.Checks) < 5 { // 4 common checks plus at least one of its own
			t.Errorf("%s: questions %d, checks %d", c.Name, len(c.Questions), len(c.Checks))
		}
	}
}

// With a model that behaves, the real tool path over the fixture satisfies the real cases.
func TestCases_ACooperativeModelPassesTheCaptainCaseOnTheFixture(t *testing.T) {
	var captain eval.Case
	for _, c := range eval.Cases() {
		if c.Name == "captain" {
			captain = c
		}
	}

	got := runner(fake{tool: "best_lineup", reply: "Captain Haaland: he has the best expected points."}, 2).Run(context.Background(), []eval.Case{captain})

	if got[0].Passes() != 2 {
		t.Errorf("%+v", got[0].Runs)
	}
}

func TestWrite_ShowsFailuresAndTheOverallRate(t *testing.T) {
	r := runner(fake{tool: "best_lineup", reply: "Captain Haaland."}, 2)
	res := r.Run(context.Background(), []eval.Case{
		{Name: "good", Questions: []string{"q"}, Checks: []eval.Check{eval.Mentions("Haaland")}},
		{Name: "bad", Questions: []string{"q"}, Checks: []eval.Check{eval.Mentions("Salah")}},
	})
	var sb strings.Builder

	eval.Write(&sb, res)

	out := sb.String()
	for _, want := range []string{"ok   good", "2/2", "FAIL bad", "0/2", "x2 mentions Salah", "Pass rate 50.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestFixture_WhatIfNamedAppliesTheSameKindsOfRule(t *testing.T) {
	f := eval.NewFixture()
	ctx := context.Background()

	ok, err := f.WhatIfNamed(ctx, service.WhatIfNamedRequest{Moves: []service.NamedMove{{Out: "hughes", In: "GOMEZ"}}})
	if err != nil || !ok.Valid || ok.Verdict != "worth_it" || ok.NetPoints != 8.8 || ok.BankAfter != "£1.6m" {
		t.Errorf("a clear upgrade: err %v %+v", err, ok)
	}
	bad, err := f.WhatIfNamed(ctx, service.WhatIfNamedRequest{Moves: []service.NamedMove{{Out: "Raya", In: "Haaland"}}})
	if err != nil || bad.Valid || len(bad.Problems) != 1 || !strings.Contains(bad.Problems[0], "like for like") {
		t.Errorf("a goalkeeper for a forward: err %v %+v", err, bad)
	}
	var in *service.InputError
	if _, err := f.WhatIfNamed(ctx, service.WhatIfNamedRequest{Moves: []service.NamedMove{{Out: "Messi", In: "Gomez"}}}); !errors.As(err, &in) {
		t.Errorf("an unknown player must be an input error, got %v", err)
	}
	broke, _ := f.WhatIfNamed(ctx, service.WhatIfNamedRequest{Moves: []service.NamedMove{{Out: "Hughes", In: "Haaland"}}})
	if broke.Valid || len(broke.Problems) == 0 {
		t.Errorf("£15.0m for £5.0m with £0.9m in the bank is over budget: %+v", broke)
	}
}
