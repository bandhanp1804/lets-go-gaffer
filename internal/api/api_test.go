package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/api"
	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

const ui = "http://localhost:3001"

// svc is a Service returning canned views and remembering the requests it saw.
type svc struct {
	viewer   service.Viewer
	squadReq service.SquadRequest
	mu       sync.Mutex
	err      error
	panics   bool
	transfr  service.TransfersRequest
	players  service.PlayerQuery
	news     service.NewsQuery
}

func (s *svc) Status(context.Context) (service.StatusView, error) {
	return service.StatusView{Gameweek: 9, DeadlineIn: "2d 3h", Clubs: []service.ClubView{{Short: "ARS", Name: "Arsenal"}}}, s.err
}
func (s *svc) BuildSquad(_ context.Context, r service.SquadRequest) (service.SquadView, error) {
	s.mu.Lock()
	s.squadReq = r
	s.mu.Unlock()
	return service.SquadView{Formation: "3-4-3", Budget: "£100.0m"}, s.err
}
func (s *svc) Backtest(context.Context) (service.BacktestView, error) {
	return service.BacktestView{FirstGameweek: 3, LastGameweek: 8}, s.err
}
func (s *svc) MyTeam(ctx context.Context) (service.MyTeamView, error) {
	if s.panics {
		panic("boom")
	}
	s.mu.Lock()
	s.viewer = service.ViewerFrom(ctx)
	s.mu.Unlock()
	return service.MyTeamView{TeamName: "Test FC", Gameweek: 9}, s.err
}
func (s *svc) Lineup(context.Context) (service.LineupView, error) {
	return service.LineupView{Formation: "4-4-2", Captain: "Haaland"}, s.err
}
func (s *svc) Transfers(_ context.Context, r service.TransfersRequest) (service.TransfersView, error) {
	s.mu.Lock()
	s.transfr = r
	s.mu.Unlock()
	return service.TransfersView{Gameweek: 9}, s.err
}
func (s *svc) Players(_ context.Context, q service.PlayerQuery) (service.PlayersView, error) {
	s.mu.Lock()
	s.players = q
	s.mu.Unlock()
	return service.PlayersView{Players: []service.PlayerView{{Name: "Rice"}}, Total: 668, Offset: q.Offset, Limit: 25}, s.err
}
func (s *svc) SearchNews(_ context.Context, q service.NewsQuery) ([]service.NewsView, error) {
	s.mu.Lock()
	s.news = q
	s.mu.Unlock()
	return []service.NewsView{{Title: "Saka doubt"}}, s.err
}

// conv is a Conversation that streams two pieces and counts its questions.
type conv struct {
	mu      sync.Mutex
	asked   []string
	err     error
	block   chan struct{} // when set, Ask waits for it (or for ctx)
	started chan struct{}
}

func (c *conv) Ask(ctx context.Context, q string, onText func(string)) (assistant.Answer, error) {
	c.mu.Lock()
	c.asked = append(c.asked, q)
	n := len(c.asked)
	c.mu.Unlock()
	if c.started != nil {
		close(c.started)
	}
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return assistant.Answer{}, ctx.Err()
		}
	}
	if c.err != nil {
		return assistant.Answer{}, c.err
	}
	onText("Cap")
	onText("tain Haaland")
	var ungrounded []string
	if n > 1 { // the second answer of a conversation carries a check warning
		ungrounded = []string{"9.9"}
	}
	return assistant.Answer{
		Text: "Captain Haaland", Rounds: 2, Usage: llm.Usage{InputTokens: 10, OutputTokens: 3},
		Sources:    []assistant.Source{{Ref: 1, Title: "Haaland trains", URL: "https://x.test/h", Source: "sky", Age: "5h"}},
		ToolCalls:  []assistant.ToolCall{{Name: "best_lineup", Input: "{}", Duration: 12 * time.Millisecond}},
		Ungrounded: ungrounded,
	}, nil
}

type rig struct {
	t     *testing.T
	svc   *svc
	convs []*conv
	srv   *api.Server
}

func newRig(t *testing.T, mutate func(*api.Config, *rig)) *rig {
	t.Helper()
	r := &rig{t: t, svc: &svc{}}
	cfg := api.Config{
		Service:        r.svc,
		AllowedOrigins: []string{ui},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewConversation: func() api.Conversation {
			c := &conv{}
			r.convs = append(r.convs, c)
			return c
		},
	}
	if mutate != nil {
		mutate(&cfg, r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := api.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.srv = srv
	return r
}

func (r *rig) do(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "localhost:8080"
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.srv.Handler().ServeHTTP(w, req)
	return w
}

func errorOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not a JSON error: %q", w.Body.String())
	}
	return e.Error
}

func TestReadEndpointsReturnJSON(t *testing.T) {
	r := newRig(t, nil)
	tests := []struct{ path, want string }{
		{"/api/healthz", `"status":"ok"`},
		{"/api/status", `"deadline_in":"2d 3h"`},
		{"/api/squad", `"formation":"3-4-3"`},
		{"/api/backtest", `"last_gameweek":8`},
		{"/api/team", `"team_name":"Test FC"`},
		{"/api/lineup", `"captain":"Haaland"`},
		{"/api/transfers", `"gameweek":9`},
		{"/api/players", `"total":668`},
		{"/api/news?q=saka", `"stories":[{`},
	}
	for _, tt := range tests {
		w := r.do("GET", tt.path, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tt.want) {
			t.Errorf("%s: %d %s, want 200 containing %s", tt.path, w.Code, w.Body, tt.want)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content type %q", tt.path, ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: a squad must not be cached by the browser, got %q", tt.path, cc)
		}
	}
}

func TestQueryParametersReachTheService(t *testing.T) {
	r := newRig(t, nil)

	r.do("GET", "/api/transfers?wildcard=true&free_transfers=2&horizon=6&max_transfers=3", "", nil)
	r.do("GET", "/api/players?position=MID&team=ARS&name=Rice&status=any&sort_by=price&sort_dir=desc&max_price=8.5&min_price=5&limit=7&offset=14", "", nil)
	r.do("GET", "/api/news?q=injury&player=Saka&days=3&limit=4", "", nil)

	two := 2
	if got := r.svc.transfr; !got.Wildcard || *got.Free != two || got.Horizon != 6 || got.Max != 3 {
		t.Errorf("transfers request = %+v", got)
	}
	if got := r.svc.players; got != (service.PlayerQuery{Position: "MID", Team: "ARS", Name: "Rice", Status: "any", SortBy: "price", SortDir: "desc", MaxPrice: 8.5, MinPrice: 5, Limit: 7, Offset: 14}) {
		t.Errorf("player query = %+v", got)
	}
	if got := r.svc.news; got != (service.NewsQuery{Query: "injury", Player: "Saka", Days: 3, Limit: 4}) {
		t.Errorf("news query = %+v", got)
	}
}

func TestSquadHorizonReachesTheService(t *testing.T) {
	r := newRig(t, nil)

	r.do("GET", "/api/squad?horizon=6", "", nil)

	if r.svc.squadReq.Horizon != 6 {
		t.Errorf("squad request = %+v", r.svc.squadReq)
	}
	if w := r.do("GET", "/api/squad?horizon=far", "", nil); w.Code != 400 || !strings.Contains(errorOf(t, w), "horizon") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestNoFreeTransfersParameterMeansTheRealCount(t *testing.T) {
	r := newRig(t, nil)

	r.do("GET", "/api/transfers", "", nil)

	if r.svc.transfr.Free != nil {
		t.Errorf("free_transfers omitted must stay nil (use the real count), got %d", *r.svc.transfr.Free)
	}
}

func TestBadParametersAre400WithAMessage(t *testing.T) {
	r := newRig(t, nil)
	tests := []struct{ path, mention string }{
		{"/api/transfers?wildcard=maybe", "wildcard"},
		{"/api/transfers?free_transfers=-1", "free_transfers"},
		{"/api/transfers?horizon=soon", "horizon"},
		{"/api/players?max_price=cheap", "max_price"},
		{"/api/players?limit=1.5", "limit"},
		{"/api/players?offset=next", "offset"},
		{"/api/news?days=x", "days"},
	}
	for _, tt := range tests {
		w := r.do("GET", tt.path, "", nil)
		if w.Code != 400 || !strings.Contains(errorOf(t, w), tt.mention) {
			t.Errorf("%s: %d %s", tt.path, w.Code, w.Body)
		}
	}
}

func TestServiceErrorsMapToStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		wantMsg    string
		mustNotSay string
	}{
		{"input error", &service.InputError{Msg: `no club named "XYZ"`}, 400, `no club named "XYZ"`, ""},
		{"wrapped input error", errors.Join(errors.New("ctx"), &service.InputError{Msg: "bad club"}), 400, "bad club", ""},
		{"news off", service.ErrNewsUnavailable, 503, "unavailable", ""},
		{"timeout", context.DeadlineExceeded, 504, "too long", ""},
		{"anything else is generic", errors.New("pq: password authentication failed for user gaffer"), 500, "internal error", "password"},
	}
	for _, tt := range tests {
		r := newRig(t, nil)
		r.svc.err = tt.err
		w := r.do("GET", "/api/lineup", "", nil)
		if w.Code != tt.status || !strings.Contains(errorOf(t, w), tt.wantMsg) {
			t.Errorf("%s: %d %s", tt.name, w.Code, w.Body)
		}
		if tt.mustNotSay != "" && strings.Contains(w.Body.String(), tt.mustNotSay) {
			t.Errorf("%s: internals leaked: %s", tt.name, w.Body)
		}
	}
}

func TestAPanicIsA500NotADroppedConnection(t *testing.T) {
	r := newRig(t, nil)
	r.svc.panics = true

	w := r.do("GET", "/api/team", "", nil)

	if w.Code != 500 || errorOf(t, w) != "internal error" {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestGuard(t *testing.T) {
	tests := []struct {
		name   string
		method string
		host   string
		hdr    map[string]string
		status int
	}{
		{"UI origin", "GET", "localhost:8080", map[string]string{"Origin": ui}, 200},
		{"no origin (curl, or the Next proxy server-side)", "GET", "127.0.0.1:8080", nil, 200},
		{"IPv6 loopback", "GET", "[::1]:8080", nil, 200},
		{"another site's origin", "GET", "localhost:8080", map[string]string{"Origin": "https://evil.example"}, 403},
		{"DNS rebinding: attacker's name points at 127.0.0.1", "GET", "evil.example:8080", nil, 403},
		{"rebinding on a look-alike", "GET", "localhost.evil.example", nil, 403},
		{"POST as a plain form (no preflight)", "POST", "localhost:8080", map[string]string{"Content-Type": "text/plain", "Origin": ui}, 415},
		{"POST from another origin", "POST", "localhost:8080", map[string]string{"Origin": "https://evil.example"}, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			path, body := "/api/lineup", ""
			if tt.method == "POST" {
				path, body = "/api/ask", `{"question":"hi"}`
			}
			req := httptest.NewRequest(tt.method, path, strings.NewReader(body))
			req.Host = tt.host
			if tt.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range tt.hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			r.srv.Handler().ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Errorf("status = %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
				t.Errorf("this server must never send CORS headers, got %q", h)
			}
		})
	}
}

func TestNewRejectsABadOrigin(t *testing.T) {
	for _, o := range []string{"localhost:3001", "http://localhost:3001/", "*"} {
		if _, err := api.New(context.Background(), api.Config{Service: &svc{}, AllowedOrigins: []string{o}}); err == nil {
			t.Errorf("origin %q was accepted", o)
		}
	}
	if _, err := api.New(context.Background(), api.Config{}); err == nil {
		t.Error("a missing Service was accepted")
	}
}

type event struct{ name, data string }

func events(t *testing.T, body string) []event {
	t.Helper()
	var out []event
	for _, blk := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var e event
		for _, line := range strings.Split(blk, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
		out = append(out, e)
	}
	return out
}

func TestAskStreamsTextThenDone(t *testing.T) {
	r := newRig(t, nil)

	w := r.do("POST", "/api/ask", `{"question":"Who should I captain?"}`, nil)

	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
	ev := events(t, w.Body.String())
	if len(ev) != 4 || ev[0].name != "session" || ev[1].name != "text" || ev[2].name != "text" || ev[3].name != "done" {
		t.Fatalf("events = %+v", ev)
	}
	if ev[1].data != `{"delta":"Cap"}` {
		t.Errorf("first delta = %s", ev[1].data)
	}
	var done struct {
		SessionID string `json:"session_id"`
		Text      string
		Sources   []struct {
			Ref        int
			Title, URL string
		}
		Warnings []string
		Tools    []struct {
			Name       string
			DurationMS int64 `json:"duration_ms"`
		}
		Rounds int
	}
	if err := json.Unmarshal([]byte(ev[3].data), &done); err != nil {
		t.Fatal(err)
	}
	if done.SessionID == "" || done.Text != "Captain Haaland" || done.Rounds != 2 {
		t.Errorf("done = %+v", done)
	}
	if len(done.Sources) != 1 || done.Sources[0].Ref != 1 || done.Sources[0].URL != "https://x.test/h" {
		t.Errorf("sources = %+v", done.Sources)
	}
	if len(done.Tools) != 1 || done.Tools[0].Name != "best_lineup" || done.Tools[0].DurationMS != 12 {
		t.Errorf("tools = %+v", done.Tools)
	}
	if done.Warnings == nil || len(done.Warnings) != 0 {
		t.Errorf("warnings must be an empty list, not null: %v", done.Warnings)
	}
}

func TestAskContinuesAConversationAndSurfacesWarnings(t *testing.T) {
	r := newRig(t, nil)
	first := r.do("POST", "/api/ask", `{"question":"one"}`, nil)
	var d struct {
		SessionID string   `json:"session_id"`
		Warnings  []string `json:"warnings"`
	}
	evs := events(t, first.Body.String())
	json.Unmarshal([]byte(evs[len(evs)-1].data), &d)

	second := r.do("POST", "/api/ask", `{"question":"two","session_id":"`+d.SessionID+`"}`, nil)

	if len(r.convs) != 1 || len(r.convs[0].asked) != 2 || r.convs[0].asked[1] != "two" {
		t.Fatalf("the follow-up must reach the same conversation: %d convs", len(r.convs))
	}
	evs = events(t, second.Body.String())
	json.Unmarshal([]byte(evs[len(evs)-1].data), &d)
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "9.9") {
		t.Errorf("the assistant's checks must reach the client: %v", d.Warnings)
	}
}

func TestAskRejectsBadRequestsBeforeStreaming(t *testing.T) {
	long := strings.Repeat("x", 2001)
	tests := []struct {
		name, body string
		status     int
	}{
		{"not JSON", `hello`, 400},
		{"empty question", `{"question":""}`, 400},
		{"too long", `{"question":"` + long + `"}`, 400},
		{"unknown field", `{"question":"hi","model":"opus"}`, 400},
		{"unknown session", `{"question":"hi","session_id":"nope"}`, 404},
	}
	for _, tt := range tests {
		r := newRig(t, nil)
		w := r.do("POST", "/api/ask", tt.body, nil)
		if w.Code != tt.status || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %s", tt.name, w.Code, w.Body)
		}
		if len(r.convs) != 0 {
			t.Errorf("%s: a conversation was created for a bad request", tt.name)
		}
	}
}

func TestAskIsOffWithoutAnAssistant(t *testing.T) {
	r := newRig(t, func(c *api.Config, _ *rig) { c.NewConversation = nil })

	w := r.do("POST", "/api/ask", `{"question":"hi"}`, nil)

	if w.Code != 503 {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestAskErrorIsAnEventWithoutInternals(t *testing.T) {
	r := newRig(t, func(c *api.Config, r *rig) {
		c.NewConversation = func() api.Conversation {
			cv := &conv{err: errors.New("anthropic 401: invalid x-api-key sk-ant-SECRET")}
			r.convs = append(r.convs, cv)
			return cv
		}
	})

	w := r.do("POST", "/api/ask", `{"question":"hi"}`, nil)

	ev := events(t, w.Body.String())
	if len(ev) != 2 || ev[0].name != "session" || ev[1].name != "error" {
		t.Fatalf("events = %+v", ev)
	}
	if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "sk-ant") {
		t.Errorf("an API error leaked to the client: %s", w.Body)
	}
}

func TestAskWhileAnotherQuestionRunsIs409AndAClosedTabFreesTheConversation(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	r := newRig(t, func(c *api.Config, r *rig) {
		c.NewConversation = func() api.Conversation {
			cv := &conv{block: block, started: started}
			r.convs = append(r.convs, cv)
			return cv
		}
	})
	srv := httptest.NewServer(r.srv.Handler())
	defer srv.Close()
	post := func(ctx context.Context, body string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/ask", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(req)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := post(ctx, `{"question":"slow"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line := make([]byte, 256)
	n, _ := resp.Body.Read(line) // the first event arrives while the model is still working
	var first struct {
		ID string `json:"session_id"`
	}
	data := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(line[:n]), "\n", 3)[1], "data: "))
	if err := json.Unmarshal([]byte(data), &first); err != nil || first.ID == "" {
		t.Fatalf("no session event first: %q", line[:n])
	}
	<-started

	busy, err := post(context.Background(), `{"question":"again","session_id":"`+first.ID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	busy.Body.Close()
	if busy.StatusCode != http.StatusConflict {
		t.Errorf("a second question in a busy conversation = %d, want 409", busy.StatusCode)
	}

	cancel() // the browser tab is closed: the model call is cancelled and the slot is freed
	deadline := time.After(2 * time.Second)
	for {
		resp2, err := post(context.Background(), `{"question":"after","session_id":"`+first.ID+`"}`)
		if err == nil {
			code := resp2.StatusCode
			resp2.Body.Close()
			if code != http.StatusConflict {
				break
			}
		}
		select {
		case <-deadline:
			t.Fatal("the conversation stayed busy after the client disconnected")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(block) // let the stub finish any call still waiting
}
