package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

func TestAge(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "0m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{3 * time.Hour, "3h"},
		{47 * time.Hour, "47h"},
		{48 * time.Hour, "2d"},
		{10 * 24 * time.Hour, "10d"},
		{-time.Minute, "0m"}, // a clock skewed into the future must not print a negative age
	}
	for _, tc := range tests {
		if got := age(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("age(%v ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("got %q, want abcd…", got)
	}
	// Cut on characters, never in the middle of a multi-byte one.
	if got := truncate("João Pedro scores", 5); got != "João…" {
		t.Errorf("got %q, want João…", got)
	}
}

func TestPickPlayer(t *testing.T) {
	teams := []fpl.Team{{ID: 1, ShortName: "MUN"}, {ID: 2, ShortName: "BHA"}}
	one := []fpl.Player{{ID: 7, WebName: "Saka"}}
	two := []fpl.Player{{ID: 20, WebName: "B.Fernandes", TeamID: 1}, {ID: 21, WebName: "J.Fernandes", TeamID: 2}}

	if ids, err := pickPlayer("saka", one, teams); err != nil || len(ids) != 1 || ids[0] != 7 {
		t.Errorf("single match = %v, %v", ids, err)
	}
	if _, err := pickPlayer("nobody", nil, teams); err == nil || !strings.Contains(err.Error(), "no player named") {
		t.Errorf("no match err = %v", err)
	}
	// Ambiguity must be an error that lists the options, not a guess.
	_, err := pickPlayer("Fernandes", two, teams)
	if err == nil || !strings.Contains(err.Error(), "B.Fernandes (MUN)") || !strings.Contains(err.Error(), "J.Fernandes (BHA)") {
		t.Errorf("ambiguous err = %v, want both candidates with their clubs", err)
	}
}

func TestPrintHits(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	hits := []db.Hit{{
		Title: "T", Content: "Saka injury doubt", URL: "https://x/1", Source: "bbc",
		PublishedAt: now.Add(-3 * time.Hour), Similarity: 0.6123, PlayerIDs: []int{10},
	}}
	var b strings.Builder

	printHits(&b, hits, []fpl.Player{{ID: 10, WebName: "Saka"}}, now)

	for _, want := range []string{"1. [0.61] 3h ago  bbc  about: Saka", "Saka injury doubt", "https://x/1"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output missing %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	printHits(&b, nil, nil, now)
	if !strings.Contains(b.String(), "no matching stories") {
		t.Errorf("empty output = %q", b.String())
	}
}
