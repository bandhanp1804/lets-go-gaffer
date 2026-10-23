package llm_test

import (
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

func TestModelFromEnv(t *testing.T) {
	tests := []struct {
		name, env, want string
	}{
		{"unset uses the default", "", llm.DefaultModel},
		{"whitespace only uses the default", "   ", llm.DefaultModel},
		{"an override wins", "claude-sonnet-5-5", "claude-sonnet-5-5"},
		{"an override is trimmed", "  claude-opus-5-5 ", "claude-opus-5-5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_MODEL", tc.env)

			if got := llm.ModelFromEnv(); got != tc.want {
				t.Errorf("ModelFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDefaultModelIsHaiku(t *testing.T) {
	// The questions this assistant answers are short and the heavy lifting is done by Go
	// tools, so the small, fast, cheap model is the default. Changing this is a decision, not a drift.
	if llm.DefaultModel != "claude-haiku-5-5" {
		t.Errorf("DefaultModel = %q, want claude-haiku-5-5", llm.DefaultModel)
	}
}
