package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

// Live test against the real API. It skips unless ANTHROPIC_API_KEY is set, so CI and
// a plain `go test ./...` never spend tokens. It sends two tiny requests.
func TestAnthropic_Live(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	hc := &http.Client{Transport: httpx.NewRetryTransport(nil, httpx.RetryConfig{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 8 * time.Second})}
	c, err := llm.NewAnthropic(hc, llm.Config{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	model := llm.ModelFromEnv()

	// 1. Streaming text.
	var pieces int
	resp, err := c.Stream(ctx, llm.Request{
		Model: model, MaxTokens: 30,
		Messages: []llm.Message{{Role: llm.User, Content: []llm.Block{llm.Text("Reply with the single word: ready")}}},
	}, func(string) { pieces++ })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp.Text()), "ready") || resp.StopReason != "end_turn" || pieces == 0 {
		t.Errorf("text %q stop %q pieces %d", resp.Text(), resp.StopReason, pieces)
	}

	// 2. A full tool round trip: the model asks, we answer, the model uses the answer.
	tool := llm.Tool{Name: "get_goal_count", Description: "Returns how many goals a player has scored.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"player":{"type":"string"}},"required":["player"]}`)}
	msgs := []llm.Message{{Role: llm.User, Content: []llm.Block{llm.Text("How many goals has Haaland scored? Use the tool.")}}}
	first, err := c.Stream(ctx, llm.Request{Model: model, MaxTokens: 200, Messages: msgs, Tools: []llm.Tool{tool}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uses := first.ToolUses()
	if first.StopReason != "tool_use" || len(uses) != 1 || uses[0].Name != "get_goal_count" {
		t.Fatalf("first reply stop %q uses %+v", first.StopReason, uses)
	}
	msgs = append(msgs,
		llm.Message{Role: llm.Assistant, Content: first.Content},
		llm.Message{Role: llm.User, Content: []llm.Block{llm.ToolResult(uses[0].ID, "7", false)}},
	)
	second, err := c.Stream(ctx, llm.Request{Model: model, MaxTokens: 200, Messages: msgs, Tools: []llm.Tool{tool}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.StopReason != "end_turn" || !strings.Contains(second.Text(), "7") {
		t.Errorf("final answer %q stop %q: the model should report the tool's 7", second.Text(), second.StopReason)
	}
	t.Logf("tokens: first %+v second %+v", first.Usage, second.Usage)
}
