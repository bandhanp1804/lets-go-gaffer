// Package eval measures the assistant. It runs a fixed set of questions against a
// model and a stub backend with known data, and judges each answer with plain Go
// checks: which tools were called, whether the grounding checks fired, what the
// answer says and omits. No model grades another model, so a failure is a fact.
package eval

import (
	"fmt"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
)

// Result is everything one run of a case produced, one Answer per question.
type Result struct {
	Answers []assistant.Answer
}

// Last is the answer to the final question.
func (r Result) Last() assistant.Answer {
	if len(r.Answers) == 0 {
		return assistant.Answer{}
	}
	return r.Answers[len(r.Answers)-1]
}

// toolCalls lists every tool call across the whole conversation.
func (r Result) toolCalls() []assistant.ToolCall {
	var out []assistant.ToolCall
	for _, a := range r.Answers {
		out = append(out, a.ToolCalls...)
	}
	return out
}

// Check is one pass/fail judgement of a Result. A nil error is a pass.
type Check struct {
	Name string
	Run  func(Result) error
}

// Custom wraps any function as a check.
func Custom(name string, f func(Result) error) Check { return Check{Name: name, Run: f} }

func calledNames(r Result) map[string]bool {
	m := map[string]bool{}
	for _, c := range r.toolCalls() {
		m[c.Name] = true
	}
	return m
}

// CalledTool passes when every named tool was called at least once.
func CalledTool(names ...string) Check {
	return Check{"called " + strings.Join(names, "+"), func(r Result) error {
		got := calledNames(r)
		for _, n := range names {
			if !got[n] {
				return fmt.Errorf("%s was not called (called: %s)", n, listTools(r))
			}
		}
		return nil
	}}
}

// NeverCalled passes when none of the named tools was called.
func NeverCalled(names ...string) Check {
	return Check{"never called " + strings.Join(names, "/"), func(r Result) error {
		got := calledNames(r)
		for _, n := range names {
			if got[n] {
				return fmt.Errorf("%s was called", n)
			}
		}
		return nil
	}}
}

// NoToolCalls passes when the model answered without any tool.
func NoToolCalls() Check {
	return Check{"no tool calls", func(r Result) error {
		if len(r.toolCalls()) > 0 {
			return fmt.Errorf("called %s", listTools(r))
		}
		return nil
	}}
}

// ToolInputHas passes when some call to the tool had arguments containing the text,
// ignoring case and whitespace so `"wildcard": true` and `"wildcard":true` agree.
func ToolInputHas(tool, text string) Check {
	squash := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	return Check{fmt.Sprintf("%s input has %s", tool, text), func(r Result) error {
		for _, c := range r.toolCalls() {
			if c.Name == tool && strings.Contains(squash(c.Input), squash(text)) {
				return nil
			}
		}
		return fmt.Errorf("no %s call had %q in its arguments", tool, text)
	}}
}

// Grounded passes when none of the assistant's own checks fired on any answer:
// no invented numbers, no citation that does not exist, no uncited news, no club
// named that the data never mentioned.
func Grounded() Check {
	return Check{"grounded", func(r Result) error {
		for _, a := range r.Answers {
			if w := a.Warnings(); len(w) > 0 {
				return fmt.Errorf("%s", strings.Join(w, " "))
			}
		}
		return nil
	}}
}

// NoToolErrors passes when every tool call succeeded.
func NoToolErrors() Check {
	return Check{"no tool errors", func(r Result) error {
		for _, c := range r.toolCalls() {
			if c.Error != "" {
				return fmt.Errorf("%s failed: %s", c.Name, c.Error)
			}
		}
		return nil
	}}
}

// NoURLs passes when the answer prints no web address; sources are listed by Go.
func NoURLs() Check {
	return Check{"no URLs in the answer", func(r Result) error {
		t := strings.ToLower(r.Last().Text)
		if strings.Contains(t, "http://") || strings.Contains(t, "https://") || strings.Contains(t, "www.") {
			return fmt.Errorf("the answer contains a web address")
		}
		return nil
	}}
}

// Cites passes when the final answer is backed by at least one retrieved story.
func Cites() Check {
	return Check{"cites a story", func(r Result) error {
		if len(r.Last().Sources) == 0 {
			return fmt.Errorf("the answer cites no story")
		}
		return nil
	}}
}

// WithinRounds passes when no question took more than n model calls.
func WithinRounds(n int) Check {
	return Check{fmt.Sprintf("at most %d model calls per question", n), func(r Result) error {
		for _, a := range r.Answers {
			if a.Rounds > n {
				return fmt.Errorf("took %d model calls", a.Rounds)
			}
		}
		return nil
	}}
}

// Mentions passes when the final answer contains every text, ignoring case.
func Mentions(texts ...string) Check {
	return Check{"mentions " + strings.Join(texts, ", "), func(r Result) error {
		t := strings.ToLower(r.Last().Text)
		for _, s := range texts {
			if !strings.Contains(t, strings.ToLower(s)) {
				return fmt.Errorf("the answer does not mention %q", s)
			}
		}
		return nil
	}}
}

// MentionsAny passes when the final answer contains at least one of the texts.
func MentionsAny(texts ...string) Check {
	return Check{"mentions one of " + strings.Join(texts, " / "), func(r Result) error {
		t := strings.ToLower(r.Last().Text)
		for _, s := range texts {
			if strings.Contains(t, strings.ToLower(s)) {
				return nil
			}
		}
		return fmt.Errorf("the answer mentions none of %q", texts)
	}}
}

// Omits passes when the final answer contains none of the texts, ignoring case.
func Omits(texts ...string) Check {
	return Check{"omits " + strings.Join(texts, ", "), func(r Result) error {
		t := strings.ToLower(r.Last().Text)
		for _, s := range texts {
			if strings.Contains(t, strings.ToLower(s)) {
				return fmt.Errorf("the answer contains %q", s)
			}
		}
		return nil
	}}
}

func listTools(r Result) string {
	var names []string
	for _, c := range r.toolCalls() {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
