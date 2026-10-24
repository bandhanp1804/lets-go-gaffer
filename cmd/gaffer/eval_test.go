package main

import (
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/eval"
)

func TestParseEvalArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    evalOpts
		wantErr bool
	}{
		{"defaults", nil, evalOpts{runs: 3, workers: 2, min: 0.9}, false},
		{"all flags", []string{"-run", "cap", "-n", "5", "-workers", "4", "-min", "0.8"}, evalOpts{"cap", 5, 4, 0.8}, false},
		{"no runs", []string{"-n", "0"}, evalOpts{}, true},
		{"min above one", []string{"-min", "90"}, evalOpts{}, true},
		{"stray argument", []string{"captain"}, evalOpts{}, true},
		{"unknown flag", []string{"-x"}, evalOpts{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEvalArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSelectCases(t *testing.T) {
	all := []eval.Case{{Name: "captain"}, {Name: "best-xi"}, {Name: "transfers"}, {Name: "transfer-prices"}}
	if got := selectCases(all, "transfer"); len(got) != 2 {
		t.Errorf("filter 'transfer' kept %d, want 2", len(got))
	}
	if got := selectCases(all, ""); len(got) != 4 {
		t.Errorf("empty filter kept %d, want all 4", len(got))
	}
	if got := selectCases(all, "zzz"); len(got) != 0 {
		t.Errorf("no match kept %d", len(got))
	}
}
