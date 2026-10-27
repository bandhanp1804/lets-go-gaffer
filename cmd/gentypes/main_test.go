package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed TypeScript must be exactly what the Go types produce. If this fails, a
// response type changed: run `go run ./cmd/gentypes` and commit the result.
func TestCommittedTypesAreUpToDate(t *testing.T) {
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", defaultOut))
	if err != nil {
		t.Fatalf("%v\nrun `go run ./cmd/gentypes` to create it", err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date: run `go run ./cmd/gentypes` and commit the result", defaultOut)
	}
}

func TestEveryResponseTypeTheUIUsesIsGenerated(t *testing.T) {
	text, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"MyTeam", "Lineup", "Transfers", "Players", "NewsSearch", "News", "NewsPlayer", "RefreshStatus", "UserSettings", "UserSettingsRequest", "SavedToken", "Alerts", "Alert", "Watchlist", "Watched", "Health", "EvalRun", "BacktestRun", "Status", "Squad", "Backtest", "WhatIf", "WhatIfMove", "WhatIfLineup", "WhatIfWeek",
		"Player", "Move", "Suggestion", "Params", "AskDone", "AskSource", "AskTool", "AskUsage"} {
		if !strings.Contains(text, "export interface "+name+" {") {
			t.Errorf("no interface %s", name)
		}
	}
}
