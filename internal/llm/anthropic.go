package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	defaultVersion = "2023-06-01"
	// maxEventBytes bounds one streamed line, so a misbehaving server cannot make us buffer without limit.
	maxEventBytes = 8 << 20
)

// Config configures the client.
type Config struct {
	APIKey  string // required
	BaseURL string // default https://api.anthropic.com; tests point it at httptest
	Version string // the anthropic-version header; default 2023-06-01
}

// Anthropic calls the Messages API.
type Anthropic struct {
	hc  *http.Client
	cfg Config
}

// NewAnthropic returns a client. Pass an *http.Client whose transport retries
// transient failures and paces requests (see package httpx). The key is sent only as
// a header and appears in no error.
func NewAnthropic(hc *http.Client, cfg Config) (*Anthropic, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("anthropic: API key is empty")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Anthropic{hc: hc, cfg: cfg}, nil
}

type cacheControl struct {
	Type string `json:"type"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type apiTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type apiRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream"`
	System    []systemBlock `json:"system,omitempty"`
	Messages  []Message     `json:"messages"`
	Tools     []apiTool     `json:"tools,omitempty"`
}

// Stream sends req and reads the reply as it is generated. onText, if not nil, is
// called with each piece of text as it arrives. The full Response is returned when
// the stream completes.
//
// If the stream fails partway the Response holds whatever had arrived, alongside
// the error: a mid-stream failure is not retried because text has already been
// delivered. The system prompt and tool list are marked cacheable, so repeated
// calls with the same prefix are billed at the cheaper cached rate.
func (a *Anthropic) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	switch {
	case req.Model == "":
		return Response{}, errors.New("anthropic: a model is required")
	case req.MaxTokens < 1:
		return Response{}, errors.New("anthropic: max tokens must be at least 1")
	case len(req.Messages) == 0:
		return Response{}, errors.New("anthropic: at least one message is required")
	}

	body := apiRequest{Model: req.Model, MaxTokens: req.MaxTokens, Stream: true, Messages: req.Messages}
	if req.System != "" {
		body.System = []systemBlock{{Type: "text", Text: req.System, CacheControl: &cacheControl{Type: "ephemeral"}}}
	}
	for i, t := range req.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tool := apiTool{Name: t.Name, Description: t.Description, InputSchema: schema}
		if i == len(req.Tools)-1 {
			tool.CacheControl = &cacheControl{Type: "ephemeral"} // one breakpoint covers all the tools
		}
		body.Tools = append(body.Tools, tool)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: encode request: %w", err)
	}

	// bytes.NewReader gives the request a GetBody, so the retry transport can resend it.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", a.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", a.cfg.Version)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := a.hc.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Response{}, apiErrorFromResponse(resp)
	}
	return readStream(resp.Body, onText)
}

// apiErrorFromResponse turns a non-200 reply into an *APIError.
func apiErrorFromResponse(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	ae := &APIError{Status: resp.StatusCode}
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error.Type != "" {
		ae.Type, ae.Message = parsed.Error.Type, parsed.Error.Message
	} else {
		ae.Type = "http_error"
		ae.Message = strings.TrimSpace(string(raw))
		if len(ae.Message) > 200 {
			ae.Message = ae.Message[:200] + "…"
		}
	}
	return ae
}

type usageJSON struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage usageJSON `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *usageJSON `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// pending is a content block still being assembled from deltas.
type pending struct {
	block Block
	args  strings.Builder // the tool arguments, arriving as JSON fragments
	done  bool
}

func readStream(body io.Reader, onText func(string)) (Response, error) {
	r := bufio.NewReaderSize(body, 64<<10)
	blocks := map[int]*pending{}
	var out Response

	assemble := func() Response {
		idx := make([]int, 0, len(blocks))
		for i := range blocks {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		out.Content = out.Content[:0]
		for _, i := range idx {
			p := blocks[i]
			if p.block.Type == TypeToolUse && !p.done {
				continue // arguments incomplete: not usable
			}
			out.Content = append(out.Content, p.block)
		}
		return out
	}

	for {
		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF // the stream ended without message_stop
			}
			return assemble(), fmt.Errorf("anthropic stream: %w", err)
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // event names, comments, blank separators
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			return assemble(), fmt.Errorf("anthropic stream: bad event: %w", err)
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				u := ev.Message.Usage
				out.Usage = Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
					CacheReadTokens: u.CacheReadTokens, CacheCreationTokens: u.CacheCreationTokens}
			}
		case "content_block_start":
			if ev.ContentBlock == nil {
				continue
			}
			p := &pending{}
			switch ev.ContentBlock.Type {
			case TypeText:
				p.block = Block{Type: TypeText, Text: ev.ContentBlock.Text}
			case TypeToolUse:
				p.block = Block{Type: TypeToolUse, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
			default:
				p.block, p.done = Block{Type: ev.ContentBlock.Type}, true
			}
			blocks[ev.Index] = p
		case "content_block_delta":
			p := blocks[ev.Index]
			if p == nil || ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if p.block.Type == TypeText && ev.Delta.Text != "" {
					p.block.Text += ev.Delta.Text
					if onText != nil {
						onText(ev.Delta.Text)
					}
				}
			case "input_json_delta":
				p.args.WriteString(ev.Delta.PartialJSON)
			} // thinking, signature, and citation deltas are not used here
		case "content_block_stop":
			p := blocks[ev.Index]
			if p == nil {
				continue
			}
			if p.block.Type == TypeToolUse {
				args := strings.TrimSpace(p.args.String())
				if args == "" {
					args = "{}" // a tool with no parameters streams no fragments
				}
				if !json.Valid([]byte(args)) {
					return assemble(), fmt.Errorf("anthropic stream: tool %q sent invalid arguments", p.block.Name)
				}
				p.block.Input = json.RawMessage(args)
			}
			p.done = true
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			return assemble(), nil
		case "error":
			if ev.Error == nil {
				return assemble(), errors.New("anthropic stream: unspecified error event")
			}
			return assemble(), &APIError{Type: ev.Error.Type, Message: ev.Error.Message}
		} // ping and event types added later are ignored
	}
}

// readLine reads one line, up to maxEventBytes, without its line ending.
func readLine(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return "", err
		}
		if sb.Len()+len(chunk) > maxEventBytes {
			return "", fmt.Errorf("event larger than %d bytes", maxEventBytes)
		}
		sb.Write(chunk)
		if !isPrefix {
			return sb.String(), nil
		}
	}
}
