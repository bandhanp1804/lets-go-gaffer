package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

const testKey = "sk-ant-test-SECRET-key-123"

// ev builds one server-sent event from a name and its JSON data.
func ev(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }

func msgStart(in, cacheRead, cacheWrite int) string {
	return ev("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"usage":{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}}}`, in, cacheRead, cacheWrite))
}
func textStart(i int) string {
	return ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i))
}
func textDelta(i int, s string) string {
	b, _ := json.Marshal(s)
	return ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, b))
}
func toolStart(i int, id, name string) string {
	return ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, i, id, name))
}
func jsonDelta(i int, frag string) string {
	b, _ := json.Marshal(frag)
	return ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, i, b))
}
func blockStop(i int) string {
	return ev("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
}
func msgDelta(stop string, out int) string {
	return ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, stop, out))
}

const (
	msgStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	ping    = "event: ping\ndata: {\"type\":\"ping\"}\n\n"
)

// server replays a canned stream and records the request it received.
type server struct {
	mu      sync.Mutex
	headers []http.Header
	bodies  []map[string]any
	paths   []string
	stream  string
	status  int
	errBody string
}

func (s *server) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.headers = append(s.headers, r.Header.Clone())
		s.bodies = append(s.bodies, body)
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		if s.status != 0 && s.status != 200 {
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(s.errBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(s.stream))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, srv *httptest.Server) *llm.Anthropic {
	t.Helper()
	c, err := llm.NewAnthropic(srv.Client(), llm.Config{APIKey: testKey, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func basicReq() llm.Request {
	return llm.Request{
		Model: "claude-test", MaxTokens: 100, System: "You are a helpful assistant.",
		Messages: []llm.Message{{Role: llm.User, Content: []llm.Block{llm.Text("hello")}}},
	}
}

func collect() (func(string), *[]string) {
	var got []string
	return func(s string) { got = append(got, s) }, &got
}

func TestStream_SendsTheDocumentedRequest(t *testing.T) {
	s := &server{stream: msgStart(10, 0, 0) + textStart(0) + textDelta(0, "ok") + blockStop(0) + msgDelta("end_turn", 2) + msgStop}
	c := client(t, s.start(t))
	req := basicReq()
	req.Tools = []llm.Tool{
		{Name: "first", Description: "d1", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "last", Description: "d2", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)},
	}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.Assistant, Content: []llm.Block{llm.ToolUse("tu_1", "first", json.RawMessage(`{"a":1}`))}},
		llm.Message{Role: llm.User, Content: []llm.Block{llm.ToolResult("tu_1", "result text", false)}},
	)

	if _, err := c.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}

	if s.paths[0] != "POST /v1/messages" {
		t.Errorf("called %q, want POST /v1/messages", s.paths[0])
	}
	h := s.headers[0]
	if h.Get("x-api-key") != testKey || h.Get("anthropic-version") != "2023-06-01" || h.Get("content-type") != "application/json" {
		t.Errorf("headers = %v", h)
	}
	b := s.bodies[0]
	if b["model"] != "claude-test" || b["max_tokens"] != float64(100) || b["stream"] != true {
		t.Errorf("body basics = %v", b)
	}
	// The system prompt and the tool list are the stable prefix: marked cacheable.
	sys := b["system"].([]any)[0].(map[string]any)
	if sys["text"] != "You are a helpful assistant." || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system = %v", sys)
	}
	tools := b["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "first" || tools[0].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tools = %v", tools)
	}
	if _, cached := tools[0].(map[string]any)["cache_control"]; cached {
		t.Error("only the last tool carries the cache breakpoint")
	}
	if tools[1].(map[string]any)["cache_control"] == nil {
		t.Error("the last tool should carry the cache breakpoint")
	}
	msgs := b["messages"].([]any)
	use := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "tu_1" || use["name"] != "first" || use["input"].(map[string]any)["a"] != float64(1) {
		t.Errorf("tool_use block = %v", use)
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "tu_1" || res["content"] != "result text" {
		t.Errorf("tool_result block = %v", res)
	}
	if _, has := res["is_error"]; has {
		t.Error("is_error should be omitted when false")
	}
}

func TestStream_DeliversTextAsItArrives(t *testing.T) {
	s := &server{stream: msgStart(25, 5, 10) + textStart(0) + ping + textDelta(0, "Hel") + textDelta(0, "lo, ") + textDelta(0, "world") +
		blockStop(0) + msgDelta("end_turn", 15) + msgStop}
	c := client(t, s.start(t))
	on, got := collect()

	resp, err := c.Stream(context.Background(), basicReq(), on)

	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*got, "|") != "Hel|lo, |world" {
		t.Errorf("deltas = %v, want them delivered one by one, in order", *got)
	}
	if resp.Text() != "Hello, world" || resp.StopReason != "end_turn" {
		t.Errorf("text %q stop %q", resp.Text(), resp.StopReason)
	}
	if resp.Usage.InputTokens != 25 || resp.Usage.OutputTokens != 15 || resp.Usage.CacheReadTokens != 5 || resp.Usage.CacheCreationTokens != 10 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestStream_AssemblesToolInputFromFragments(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + textStart(0) + textDelta(0, "Let me check.") + blockStop(0) +
		toolStart(1, "toolu_1", "get_player") + jsonDelta(1, `{"na`) + jsonDelta(1, `me":"Sa`) + jsonDelta(1, `ka","limit":3}`) + blockStop(1) +
		msgDelta("tool_use", 20) + msgStop}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop = %q", resp.StopReason)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].ID != "toolu_1" || uses[0].Name != "get_player" {
		t.Fatalf("tool uses = %+v", uses)
	}
	var in struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(uses[0].Input, &in); err != nil || in.Name != "Saka" || in.Limit != 3 {
		t.Errorf("input = %s (%v)", uses[0].Input, err)
	}
	// Text and tool use keep the order the model produced them in.
	if len(resp.Content) != 2 || resp.Content[0].Type != llm.TypeText || resp.Content[1].Type != llm.TypeToolUse {
		t.Errorf("blocks = %+v", resp.Content)
	}
}

func TestStream_AToolWithNoArgumentsGetsAnEmptyObject(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + toolStart(0, "toolu_2", "get_my_team") + blockStop(0) + msgDelta("tool_use", 5) + msgStop}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.ToolUses()[0].Input); got != "{}" {
		t.Errorf("input = %q, want {}: an empty body would be invalid JSON to send back", got)
	}
}

func TestStream_ParallelToolCallsKeepTheirOwnInputs(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) +
		toolStart(0, "a", "one") + toolStart(1, "b", "two") +
		jsonDelta(0, `{"x":`) + jsonDelta(1, `{"y":2}`) + jsonDelta(0, `1}`) + blockStop(0) + blockStop(1) +
		msgDelta("tool_use", 9) + msgStop}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil {
		t.Fatal(err)
	}
	uses := resp.ToolUses()
	if len(uses) != 2 || string(uses[0].Input) != `{"x":1}` || string(uses[1].Input) != `{"y":2}` {
		t.Errorf("tool uses = %+v", uses)
	}
}

func TestStream_HandlesVeryLongEvents(t *testing.T) {
	// A tool argument or a long paragraph can exceed the 64KB default of a line scanner.
	big := strings.Repeat("a", 300_000)
	s := &server{stream: msgStart(1, 0, 0) + textStart(0) + textDelta(0, big) + blockStop(0) + msgDelta("end_turn", 3) + msgStop}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil || len(resp.Text()) != 300_000 {
		t.Errorf("err = %v, text length = %d, want 300000", err, len(resp.Text()))
	}
}

func TestStream_IgnoresEventsItDoesNotUnderstand(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + textStart(0) +
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`) +
		ev("something_new", `{"type":"something_new","x":1}`) +
		textDelta(0, "fine") + blockStop(0) + msgDelta("end_turn", 2) + msgStop}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil || resp.Text() != "fine" {
		t.Errorf("err = %v, text = %q", err, resp.Text())
	}
}

func TestStream_AnErrorEventMidStreamIsReturnedWithWhatArrived(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + textStart(0) + textDelta(0, "partial ") +
		ev("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)}
	c := client(t, s.start(t))

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Type != "overloaded_error" || ae.Message != "Overloaded" {
		t.Fatalf("err = %v, want an overloaded APIError", err)
	}
	if resp.Text() != "partial " {
		t.Errorf("partial text = %q, want what was streamed before the failure", resp.Text())
	}
}

func TestStream_ATruncatedStreamIsAnError(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + textStart(0) + textDelta(0, "cut off")} // no message_stop
	c := client(t, s.start(t))

	_, err := c.Stream(context.Background(), basicReq(), nil)

	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF: a stream that just stops must not look complete", err)
	}
}

func TestStream_MalformedEventDataIsAnError(t *testing.T) {
	s := &server{stream: msgStart(1, 0, 0) + "event: content_block_delta\ndata: {not json\n\n"}
	c := client(t, s.start(t))

	if _, err := c.Stream(context.Background(), basicReq(), nil); err == nil {
		t.Error("want an error for unparseable event data")
	}
}

func TestStream_HTTPErrorsAreTypedAndNeverLeakTheKey(t *testing.T) {
	s := &server{status: 401, errBody: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`}
	c := client(t, s.start(t))

	_, err := c.Stream(context.Background(), basicReq(), nil)

	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 401 || ae.Type != "authentication_error" || !strings.Contains(ae.Message, "invalid x-api-key") {
		t.Fatalf("err = %v, want a typed 401", err)
	}
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.StatusCode != 401 {
		t.Errorf("the status should also be reachable as an httpx.StatusError: %v", err)
	}
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error %q leaks the API key", err)
	}
}

func TestStream_NonJSONErrorBodiesStillProduceAnError(t *testing.T) {
	s := &server{status: 502, errBody: "<html>bad gateway</html>"}
	c := client(t, s.start(t))

	_, err := c.Stream(context.Background(), basicReq(), nil)

	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 502 {
		t.Errorf("err = %v, want an APIError carrying the 502", err)
	}
}

func TestStream_RetriesOverloadWithTheRequestBodyIntact(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		bodies = append(bodies, string(raw))
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
			return
		}
		_, _ = w.Write([]byte(msgStart(1, 0, 0) + textStart(0) + textDelta(0, "ok") + blockStop(0) + msgDelta("end_turn", 1) + msgStop))
	}))
	t.Cleanup(srv.Close)
	hc := &http.Client{Transport: httpx.NewRetryTransport(nil, httpx.RetryConfig{
		MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
	})}
	c, _ := llm.NewAnthropic(hc, llm.Config{APIKey: testKey, BaseURL: srv.URL})

	resp, err := c.Stream(context.Background(), basicReq(), nil)

	if err != nil || resp.Text() != "ok" {
		t.Fatalf("err = %v text = %q", err, resp.Text())
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[1], `"hello"`) {
		t.Errorf("the retry must resend the full body; got %d attempts, second body %q", len(bodies), bodies[len(bodies)-1])
	}
}

func TestStream_ContextCancelStopsAStalledStream(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(msgStart(1, 0, 0)))
		w.(http.Flusher).Flush()
		<-block
	}))
	t.Cleanup(func() { close(block); srv.Close() })
	c := client(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Stream(ctx, basicReq(), nil)

	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("took too long to stop")
	}
}

func TestStream_RejectsBadRequestsBeforeSendingAnything(t *testing.T) {
	s := &server{stream: msgStop}
	c := client(t, s.start(t))
	tests := map[string]func(*llm.Request){
		"no model":      func(r *llm.Request) { r.Model = "" },
		"no max tokens": func(r *llm.Request) { r.MaxTokens = 0 },
		"no messages":   func(r *llm.Request) { r.Messages = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := basicReq()
			mutate(&req)

			if _, err := c.Stream(context.Background(), req, nil); err == nil {
				t.Error("want an error")
			}
			if len(s.paths) != 0 {
				t.Errorf("a request was sent for an invalid call: %v", s.paths)
			}
		})
	}
}

func TestNewAnthropic_ValidatesConfigWithoutEchoingTheKey(t *testing.T) {
	if _, err := llm.NewAnthropic(nil, llm.Config{}); err == nil {
		t.Error("want an error for a missing key")
	}
	c, err := llm.NewAnthropic(nil, llm.Config{APIKey: testKey})
	if err != nil || c == nil {
		t.Errorf("a key alone should suffice: %v", err)
	}
}

func TestBlocks_RoundTripThroughJSON(t *testing.T) {
	in := []llm.Block{
		llm.Text("hi"),
		llm.ToolUse("tu_1", "f", json.RawMessage(`{"a":[1,2]}`)),
		llm.ToolResult("tu_1", "out", true),
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out []llm.Block
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	if len(out) != 3 || out[0].Text != "hi" || out[1].Name != "f" || string(out[1].Input) != `{"a":[1,2]}` ||
		out[2].ToolUseID != "tu_1" || out[2].Content != "out" || !out[2].IsError {
		t.Errorf("round trip = %+v", out)
	}
}

func TestResponse_Helpers(t *testing.T) {
	r := llm.Response{Content: []llm.Block{llm.Text("a"), llm.ToolUse("1", "x", nil), llm.Text("b"), llm.ToolUse("2", "y", nil)}}

	if r.Text() != "ab" {
		t.Errorf("Text() = %q, want the text blocks joined", r.Text())
	}
	if uses := r.ToolUses(); len(uses) != 2 || uses[0].ID != "1" || uses[1].ID != "2" {
		t.Errorf("ToolUses() = %+v", uses)
	}
}
