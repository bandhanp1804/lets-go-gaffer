package main

import (
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

func TestFormatPrice(t *testing.T) {
	tests := []struct {
		tenths int
		want   string
	}{
		{61, "£6.1m"},
		{100, "£10.0m"},
		{1000, "£100.0m"},
		{40, "£4.0m"},
		{45, "£4.5m"},
		{0, "£0.0m"},
	}
	for _, tc := range tests {
		if got := formatPrice(tc.tenths); got != tc.want {
			t.Errorf("formatPrice(%d) = %q, want %q", tc.tenths, got, tc.want)
		}
	}
}

func TestPrintSquad(t *testing.T) {
	s := fpl.Squad{
		Players: []fpl.Player{
			{ID: 1, WebName: "Raya", TeamID: 1, Position: fpl.GKP, NowCost: 61, Form: "6.0"},
			{ID: 2, WebName: "Haaland", TeamID: 2, Position: fpl.FWD, NowCost: 157, Form: "9.5"},
			{ID: 3, WebName: "Benchy", TeamID: 1, Position: fpl.DEF, NowCost: 40, Form: "1.0"},
		},
		Starting: []int{1, 2}, Captain: 2, ViceCaptain: 1,
	}
	var b strings.Builder

	printSquad(&b, s, []fpl.Team{{ID: 1, ShortName: "ARS"}, {ID: 2, ShortName: "MCI"}})

	out := b.String()
	for _, want := range []string{"Haaland", "MCI", "£15.7m", "(C)", "(VC)", "Squad cost £25.8m of £100.0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The bench player must be listed after the bench heading, not in the XI.
	if xi, bench := strings.Index(out, "Starting XI"), strings.Index(out, "Bench"); !(xi < strings.Index(out, "Raya") && strings.Index(out, "Raya") < bench && bench < strings.Index(out, "Benchy")) {
		t.Errorf("players are under the wrong heading:\n%s", out)
	}
}
