// Package llm is a small client for Anthropic's Messages API: streaming, and tool
// use. It speaks plain HTTP so the project needs no SDK.
package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// Role is who a message is from.
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Block types this client reads and writes.
const (
	TypeText       = "text"
	TypeToolUse    = "tool_use"
	TypeToolResult = "tool_result"

	// Thinking blocks must be returned to the API unchanged when a turn continues after a
	// tool call, so they are kept, but they are never shown.
	TypeThinking         = "thinking"
	TypeRedactedThinking = "redacted_thinking"
)

// Block is one piece of a message: text, a request from the model to run a tool,
// or the result of running one. Only the fields for its Type are used.
type Block struct {
	Type string

	Text string // text

	ID    string          // tool_use: the id the result must echo
	Name  string          // tool_use
	Input json.RawMessage // tool_use: the arguments, always valid JSON

	ToolUseID string // tool_result
	Content   string // tool_result
	IsError   bool   // tool_result

	Thinking  string // thinking: the model's reasoning, kept only to send back
	Signature string // thinking: proves the block is the model's own; must be returned with it
	Data      string // redacted_thinking: encrypted reasoning, returned as is
}

// Text is a text block.
func Text(s string) Block { return Block{Type: TypeText, Text: s} }

// ToolUse is the model's request to run a tool.
func ToolUse(id, name string, input json.RawMessage) Block {
	return Block{Type: TypeToolUse, ID: id, Name: name, Input: input}
}

// Thinking is a thinking block, for replaying the model's own reasoning.
func Thinking(thinking, signature string) Block {
	return Block{Type: TypeThinking, Thinking: thinking, Signature: signature}
}

// ToolResult answers a tool_use. isError tells the model the tool failed.
func ToolResult(toolUseID, content string, isError bool) Block {
	return Block{Type: TypeToolResult, ToolUseID: toolUseID, Content: content, IsError: isError}
}

// MarshalJSON writes the shape the API expects for each block type.
func (b Block) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case TypeText:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})
	case TypeToolUse:
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{b.Type, b.ID, b.Name, input})
	case TypeToolResult:
		return json.Marshal(struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
			IsError   bool   `json:"is_error,omitempty"`
		}{b.Type, b.ToolUseID, b.Content, b.IsError})
	case TypeThinking:
		return json.Marshal(struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}{b.Type, b.Thinking, b.Signature})
	case TypeRedactedThinking:
		return json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{b.Type, b.Data})
	}
	return nil, fmt.Errorf("llm: cannot encode a block of type %q", b.Type)
}

// UnmarshalJSON reads a block. Types this client does not use are kept by Type only.
func (b *Block) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   string          `json:"content"`
		IsError   bool            `json:"is_error"`
		Thinking  string          `json:"thinking"`
		Signature string          `json:"signature"`
		Data      string          `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = Block{Type: raw.Type, Text: raw.Text, ID: raw.ID, Name: raw.Name, Input: raw.Input,
		ToolUseID: raw.ToolUseID, Content: raw.Content, IsError: raw.IsError,
		Thinking: raw.Thinking, Signature: raw.Signature, Data: raw.Data}
	return nil
}

// Message is one turn of the conversation.
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

// Tool describes a function the model may ask to run. InputSchema is a JSON Schema
// for its arguments.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Request is one call to the model.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// Usage is the token accounting for one response.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int // input served from the prompt cache
	CacheCreationTokens int // input written to the prompt cache
}

// Response is the model's reply.
type Response struct {
	Content    []Block
	StopReason string // "end_turn", "tool_use", "max_tokens", ...
	Usage      Usage
}

// Text is the response's text blocks joined.
func (r Response) Text() string {
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Type == TypeText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ToolUses are the tools the model asked to run, in order.
func (r Response) ToolUses() []Block {
	var out []Block
	for _, b := range r.Content {
		if b.Type == TypeToolUse {
			out = append(out, b)
		}
	}
	return out
}

// APIError is an error reported by the API, either as an HTTP status or as an
// error event inside a stream. It never carries the request or the key.
type APIError struct {
	Status  int    // HTTP status, or 0 for an error event inside a stream
	Type    string // e.g. "overloaded_error", "authentication_error"
	Message string
}

func (e *APIError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("anthropic: %d %s: %s", e.Status, e.Type, e.Message)
	}
	return fmt.Sprintf("anthropic: %s: %s", e.Type, e.Message)
}

// Unwrap exposes the status as an httpx.StatusError, so callers can treat this like
// any other HTTP failure.
func (e *APIError) Unwrap() error {
	if e.Status == 0 {
		return nil
	}
	return &httpx.StatusError{StatusCode: e.Status}
}

// DefaultModel is the model used unless ANTHROPIC_MODEL says otherwise. The
// assistant's questions are short and Go does the computing, so the small, fast,
// inexpensive model is enough.
const DefaultModel = "claude-haiku-5-5"

// ModelFromEnv returns ANTHROPIC_MODEL if set, else DefaultModel.
func ModelFromEnv() string {
	if m := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); m != "" {
		return m
	}
	return DefaultModel
}
