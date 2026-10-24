// Package assistant answers a manager's questions by letting a language model call
// read-only tools backed by Go code, and then checking what it says.
//
// The division of labour is the point of the design. Go computes: expected points,
// prices, hits, lineups, and transfer rankings all come from tested code. The model
// only decides which tool to call and narrates the result. It cannot be trusted with
// arithmetic or with links, so it is not given the chance: numbers arrive
// pre-computed, news arrives as reference numbers without URLs, and the answer is
// afterwards checked for invented citations and for numbers no tool produced.
package assistant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

// Model is the language model. *llm.Anthropic satisfies it.
type Model interface {
	Stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error)
}

// Config tunes an Assistant. Every field has a default.
type Config struct {
	Model       string        // default llm.DefaultModel
	MaxTokens   int           // per model call; default 4000 (the model's reasoning counts too)
	MaxRounds   int           // model calls per question, so a tool loop cannot run away; default 6
	ToolTimeout time.Duration // per tool call; default 90s (a news search may wait on a rate limiter)
}

// Assistant is stateless and safe for concurrent use; conversations live in Sessions.
type Assistant struct {
	model   Model
	backend Backend
	cfg     Config
	byName  map[string]tool
	specs   []llm.Tool
}

// New returns an Assistant.
func New(m Model, b Backend, cfg Config) *Assistant {
	if cfg.Model == "" {
		cfg.Model = llm.DefaultModel
	}
	if cfg.MaxTokens < 1 {
		cfg.MaxTokens = 4000
	}
	if cfg.MaxRounds < 1 {
		cfg.MaxRounds = 6
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = 90 * time.Second
	}
	a := &Assistant{model: m, backend: b, cfg: cfg, byName: map[string]tool{}}
	for _, t := range tools() {
		a.byName[t.spec.Name] = t
		a.specs = append(a.specs, t.spec)
	}
	return a
}

// Source is a story an answer cites. Go fills it in from its own record of what was
// retrieved, never from the model's text.
type Source struct {
	Ref    int
	Title  string
	URL    string
	Source string
	Age    string
}

// ToolCall records one tool the model asked for.
type ToolCall struct {
	Name     string
	Input    string
	Output   string
	Error    string
	Duration time.Duration
}

// Answer is the result of one question.
type Answer struct {
	Text      string // everything the model said, as streamed
	Sources   []Source
	ToolCalls []ToolCall
	Usage     llm.Usage
	Rounds    int // model calls made

	// What the checks found; Warnings describes them.
	BadCitations []int    // cited references that do not exist
	UncitedNews  bool     // news was retrieved but no story is cited
	Ungrounded   []string // numbers in the answer that no tool produced
	Truncated    bool     // the model hit its token limit
}

// Session is one conversation. It remembers earlier turns and the reference numbers
// handed out for news, so a follow-up can cite and build on earlier answers.
type Session struct {
	a *Assistant

	mu       sync.Mutex
	messages []llm.Message
	refs     *refs
	known    map[string]bool // numbers the manager or a tool has produced, normalised
}

// NewSession starts an empty conversation.
func (a *Assistant) NewSession() *Session {
	return &Session{a: a, refs: newRefs(), known: map[string]bool{}}
}

// Ask answers a question, streaming text to onText (which may be nil) as it is
// generated. If the model fails, the error is returned with whatever had arrived,
// and the conversation is left as it was before the question, so the next question
// starts from a consistent state.
func (s *Session) Ask(ctx context.Context, question string, onText func(string)) (Answer, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return Answer{}, errors.New("a question is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Work on copies; they replace the session's state only if the turn succeeds.
	msgs := append(append([]llm.Message(nil), s.messages...), llm.Message{Role: llm.User, Content: []llm.Block{llm.Text(question)}})
	known := make(map[string]bool, len(s.known))
	for k := range s.known {
		known[k] = true
	}
	for _, n := range numbersIn(question) {
		known[normalise(n)] = true
	}

	var (
		ans      Answer
		text     strings.Builder
		returned = s.refs.count()
	)
	collect := func(piece string) {
		text.WriteString(piece)
		if onText != nil {
			onText(piece)
		}
	}
	addUsage := func(u llm.Usage) {
		ans.Usage.InputTokens += u.InputTokens
		ans.Usage.OutputTokens += u.OutputTokens
		ans.Usage.CacheReadTokens += u.CacheReadTokens
		ans.Usage.CacheCreationTokens += u.CacheCreationTokens
	}

	for round := 1; round <= s.a.cfg.MaxRounds; round++ {
		ans.Rounds = round
		if err := ctx.Err(); err != nil {
			ans.Text = text.String()
			return ans, err
		}
		resp, err := s.a.model.Stream(ctx, llm.Request{
			Model: s.a.cfg.Model, System: systemPrompt, Messages: msgs, Tools: s.a.specs, MaxTokens: s.a.cfg.MaxTokens,
		}, collect)
		addUsage(resp.Usage)
		if err != nil {
			ans.Text = text.String()
			return ans, fmt.Errorf("ask: %w", err)
		}
		if len(resp.Content) == 0 {
			ans.Text = text.String()
			return ans, errors.New("ask: the model returned an empty reply")
		}
		msgs = append(msgs, llm.Message{Role: llm.Assistant, Content: resp.Content})

		uses := resp.ToolUses()
		if len(uses) == 0 {
			ans.Text = text.String()
			ans.Truncated = resp.StopReason == "max_tokens"
			var bad []int
			ans.Sources, bad = citations(ans.Text, s.refs)
			ans.BadCitations = bad
			ans.UncitedNews = s.refs.count() > returned && len(ans.Sources) == 0
			ans.Ungrounded = ungrounded(ans.Text, known)
			s.messages, s.known = msgs, known
			return ans, nil
		}
		if round == s.a.cfg.MaxRounds {
			break // the model still wants tools but the budget is spent
		}

		results, calls := s.runTools(ctx, uses)
		ans.ToolCalls = append(ans.ToolCalls, calls...)
		for i, c := range calls {
			if c.Error == "" {
				for _, n := range numbersIn(results[i].Content) {
					known[normalise(n)] = true
				}
			}
		}
		msgs = append(msgs, llm.Message{Role: llm.User, Content: results})
	}
	ans.Text = text.String()
	return ans, fmt.Errorf("ask: stopped after %d rounds of tool calls without a final answer", s.a.cfg.MaxRounds)
}

// runTools runs the requested tools at the same time and returns results in the order asked.
func (s *Session) runTools(ctx context.Context, uses []llm.Block) ([]llm.Block, []ToolCall) {
	results := make([]llm.Block, len(uses))
	calls := make([]ToolCall, len(uses))
	var wg sync.WaitGroup
	for i, u := range uses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], calls[i] = s.runTool(ctx, u)
		}()
	}
	wg.Wait()
	return results, calls
}

func (s *Session) runTool(ctx context.Context, use llm.Block) (llm.Block, ToolCall) {
	start := time.Now()
	call := ToolCall{Name: use.Name, Input: string(use.Input)}
	fail := func(err error) (llm.Block, ToolCall) {
		call.Error, call.Duration = err.Error(), time.Since(start)
		return llm.ToolResult(use.ID, "Error: "+err.Error(), true), call
	}

	t, ok := s.a.byName[use.Name]
	if !ok {
		names := make([]string, 0, len(s.a.byName))
		for n := range s.a.byName {
			names = append(names, n)
		}
		sort.Strings(names)
		return fail(fmt.Errorf("unknown tool %q; the tools are %s", use.Name, strings.Join(names, ", ")))
	}
	ctx, cancel := context.WithTimeout(ctx, s.a.cfg.ToolTimeout)
	defer cancel()

	out, err := safely(func() (string, error) { return t.run(ctx, s.a.backend, use.Input, s.refs) })
	if err != nil {
		return fail(err)
	}
	out = capOutput(out)
	call.Output, call.Duration = out, time.Since(start)
	return llm.ToolResult(use.ID, out, false), call
}

// safely turns a panic in a tool into an error the model can read, so one broken tool
// cannot take down the conversation.
func safely(f func() (string, error)) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return f()
}
