package main

import (
	"strings"
	"testing"
)

func TestParseWorkflowArgs(t *testing.T) {
	tests := []struct {
		args    []string
		want    workflowOpts
		wantErr string
	}{
		{[]string{"news"}, workflowOpts{name: "news"}, ""},
		{[]string{"-detach", "eval"}, workflowOpts{name: "eval", detach: true}, ""},
		{[]string{"sync", "-force"}, workflowOpts{name: "sync", force: true}, ""},
		{[]string{"news", "-force"}, workflowOpts{}, "unexpected"}, // only sync takes -force
		{[]string{"tea"}, workflowOpts{}, "no workflow called"},
		{nil, workflowOpts{}, "usage"},
	}
	for _, tt := range tests {
		got, err := parseWorkflowArgs(tt.args)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%v: err = %v, want %q", tt.args, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%v = %+v, %v; want %+v", tt.args, got, err, tt.want)
		}
	}
}

func TestEveryManualWorkflowHasArgumentsAndAResult(t *testing.T) {
	for name, mw := range manualWorkflows() {
		if mw.fn == nil || mw.args == nil || mw.result == nil {
			t.Errorf("%s is incomplete", name)
		}
	}
}
