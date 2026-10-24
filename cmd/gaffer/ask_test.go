package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

func TestPrintAnswerExtras_ListsSourcesWithTheirLinks(t *testing.T) {
	ans := assistant.Answer{Sources: []assistant.Source{
		{Ref: 1, Title: "Saka injury doubt", Source: "bbc", Age: "2h", URL: "https://x/saka"},
		{Ref: 3, Title: "Haaland trains", Source: "sky", Age: "5h", URL: "https://x/haaland"},
	}}
	var b strings.Builder

	printAnswerExtras(&b, ans, false)

	out := b.String()
	for _, want := range []string{"Sources", "[1] Saka injury doubt", "bbc", "2h ago", "https://x/saka", "[3] Haaland trains", "https://x/haaland"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintAnswerExtras_ShowsTheChecksWhenTheyFindSomething(t *testing.T) {
	ans := assistant.Answer{BadCitations: []int{9}, Ungrounded: []string{"99.5"}}
	var b strings.Builder

	printAnswerExtras(&b, ans, false)

	out := b.String()
	if !strings.Contains(out, "Checks") || !strings.Contains(out, "[9]") || !strings.Contains(out, "99.5") {
		t.Errorf("a failed check must be visible to the manager:\n%s", out)
	}
}

func TestPrintAnswerExtras_SaysNothingWhenThereIsNothingToSay(t *testing.T) {
	var b strings.Builder

	printAnswerExtras(&b, assistant.Answer{Text: "Haaland."}, false)

	if strings.TrimSpace(b.String()) != "" {
		t.Errorf("a clean answer should add nothing: %q", b.String())
	}
}

func TestPrintAnswerExtras_VerboseShowsToolsAndTokens(t *testing.T) {
	ans := assistant.Answer{
		Rounds: 2,
		ToolCalls: []assistant.ToolCall{
			{Name: "get_my_team", Input: "{}", Duration: 120 * time.Millisecond},
			{Name: "find_players", Input: `{"position":"MID"}`, Error: "no club named ZZZ", Duration: 3 * time.Millisecond},
		},
		Usage: llm.Usage{InputTokens: 1200, OutputTokens: 150, CacheReadTokens: 800},
	}
	var quiet, loud strings.Builder

	printAnswerExtras(&quiet, ans, false)
	printAnswerExtras(&loud, ans, true)

	if strings.Contains(quiet.String(), "get_my_team") {
		t.Errorf("tool calls are shown only with -v:\n%s", quiet.String())
	}
	out := loud.String()
	for _, want := range []string{"get_my_team", "find_players", `{"position":"MID"}`, "no club named ZZZ", "1200", "150", "800", "2 model calls"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q:\n%s", want, out)
		}
	}
}

func TestParseAskArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantQ       string
		wantVerbose bool
		wantErr     bool
	}{
		{"a question", []string{"who", "should", "I", "captain?"}, "who should I captain?", false, false},
		{"verbose flag", []string{"-v", "any", "news?"}, "any news?", true, false},
		{"no question", nil, "", false, true},
		{"only a flag", []string{"-v"}, "", true, true},
		{"unknown flag", []string{"-x", "q"}, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, verbose, err := parseAskArgs(tc.args)

			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tc.wantErr)
			}
			if !tc.wantErr && (q != tc.wantQ || verbose != tc.wantVerbose) {
				t.Errorf("q %q verbose %v; want %q %v", q, verbose, tc.wantQ, tc.wantVerbose)
			}
		})
	}
}

func TestPrintAnswerExtras_AnOfficialFPLNoteIsNotShownAsALink(t *testing.T) {
	// FPL's per-player notes carry an internal identifier, not a web address.
	ans := assistant.Answer{Sources: []assistant.Source{
		{Ref: 3, Title: "Saka (Arsenal midfielder): Thigh injury - 50% chance of playing", Source: "fpl", Age: "2d", URL: "fpl://player/12/2026-10-21T20:30:09Z"},
	}}
	var b strings.Builder

	printAnswerExtras(&b, ans, false)

	out := b.String()
	if strings.Contains(out, "fpl://") {
		t.Errorf("an internal identifier was printed as if it were a link:\n%s", out)
	}
	if !strings.Contains(out, "official FPL player note") || !strings.Contains(out, "[3] Saka") {
		t.Errorf("want the source named as an official FPL note:\n%s", out)
	}
}
