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
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

const ui = "http://localhost:3001"

// svc is a Service returning canned views and remembering the requests it saw.
type svc struct {
	lineupReq service.LineupRequest
	chipsReq  service.ChipsRequest
	whatIfReq service.WhatIfRequest
	formation string
	viewer    service.Viewer
	subsReq   service.SubsRequest
	squadReq  service.SquadRequest
	mu        sync.Mutex
	err       error
	panics    bool
	transfr   service.TransfersRequest
	players   service.PlayerQuery
	news      service.NewsQuery
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
func (s *svc) Subs(_ context.Context, r service.SubsRequest) (service.SubsView, error) {
	s.mu.Lock()
	s.subsReq = r
	s.mu.Unlock()
	return service.SubsView{Valid: true, Formation: "3-5-2", Problems: []string{}}, s.err
}
func (s *svc) WhatIf(_ context.Context, r service.WhatIfRequest) (service.WhatIfView, error) {
	s.mu.Lock()
	s.whatIfReq = r
	s.mu.Unlock()
	return service.WhatIfView{Gameweek: 9, Valid: true, NetPoints: 1.5, Verdict: "worth_it"}, s.err
}
func (s *svc) ChipsFor(_ context.Context, r service.ChipsRequest) (service.ChipsView, error) {
	s.mu.Lock()
	s.chipsReq = r
	s.mu.Unlock()
	return service.ChipsView{FirstGameweek: 9, Gameweeks: 8, Chips: []service.ChipAdviceView{{Chip: "3xc", Name: "Triple Captain", Verdict: "wait", BestGameweek: 11}}}, s.err
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
func (s *svc) LineupWith(_ context.Context, r service.LineupRequest) (service.LineupView, error) {
	s.mu.Lock()
	s.formation, s.lineupReq = r.Formation, r
	s.mu.Unlock()
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
func (s *svc) Search(_ context.Context, q service.NewsQuery) (service.NewsSearchView, error) {
	s.mu.Lock()
	s.news = q
	s.mu.Unlock()
	return service.NewsSearchView{Stories: []service.NewsView{{Title: "Saka doubt"}}, Notes: []string{}}, s.err
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
		{"/api/chips", `"verdict":"wait"`},
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
	r.do("GET", "/api/news?q=injury&player=Saka&days=3&limit=4&detect=off", "", nil)

	two := 2
	if got := r.svc.transfr; !got.Wildcard || *got.Free != two || got.Horizon != 6 || got.Max != 3 {
		t.Errorf("transfers request = %+v", got)
	}
	if got := r.svc.players; got != (service.PlayerQuery{Position: "MID", Team: "ARS", Name: "Rice", Status: "any", SortBy: "price", SortDir: "desc", MaxPrice: 8.5, MinPrice: 5, Limit: 7, Offset: 14}) {
		t.Errorf("player query = %+v", got)
	}
	if got := r.svc.news; got != (service.NewsQuery{Query: "injury", Player: "Saka", Days: 3, Limit: 4, NoDetect: true}) {
		t.Errorf("news query = %+v", got)
	}
}

func TestLineupFormationReachesTheService(t *testing.T) {
	r := newRig(t, nil)

	r.do("GET", "/api/lineup?formation=3-4-3", "", nil)
	if r.svc.formation != "3-4-3" {
		t.Errorf("formation = %q", r.svc.formation)
	}
	r.do("GET", "/api/lineup", "", nil)
	if r.svc.formation != "" {
		t.Errorf("no parameter means the best formation, got %q", r.svc.formation)
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

func TestWhatIfBodyReachesTheService(t *testing.T) {
	r := newRig(t, nil)
	free := 2

	w := r.do("POST", "/api/whatif", `{"moves":[{"out":101,"in":201},{"out":102,"in":202}],"horizon":6,"free_transfers":2}`, nil)

	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verdict":"worth_it"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := r.svc.whatIfReq
	if len(got.Moves) != 2 || got.Moves[1] != (service.WhatIfMoveRequest{Out: 102, In: 202}) || got.Horizon != 6 || got.Free == nil || *got.Free != free {
		t.Errorf("request = %+v", got)
	}
}

func TestWhatIfRejectsBadBodiesAndMapsServiceInputErrors(t *testing.T) {
	r := newRig(t, nil)
	for _, body := range []string{`nope`, `{"moves":[{"out":1,"in":2,"extra":3}]}`, `{"moves":"x"}`, `{"moves":[{"out":"a","in":2}]}`} {
		if w := r.do("POST", "/api/whatif", body, nil); w.Code != 400 {
			t.Errorf("%s: status %d", body, w.Code)
		}
	}
	r.svc.err = &service.InputError{Msg: "horizon must be between 1 and 10 gameweeks"}
	if w := r.do("POST", "/api/whatif", `{"moves":[]}`, nil); w.Code != 400 || !strings.Contains(errorOf(t, w), "horizon") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestWhatIfIsAPostOnly(t *testing.T) {
	if w := newRig(t, nil).do("GET", "/api/whatif", "", nil); w.Code != 405 {
		t.Errorf("GET = %d, want 405", w.Code)
	}
}

func TestLineupCaptainAndViceReachTheService(t *testing.T) {
	r := newRig(t, nil)

	r.do("GET", "/api/lineup?formation=3-5-2&captain=14&vice=22", "", nil)

	if got := r.svc.lineupReq; got != (service.LineupRequest{Formation: "3-5-2", Captain: 14, Vice: 22}) {
		t.Errorf("request = %+v", got)
	}
	if w := r.do("GET", "/api/lineup?captain=salah", "", nil); w.Code != 400 || !strings.Contains(errorOf(t, w), "captain") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := r.do("GET", "/api/lineup?vice=x", "", nil); w.Code != 400 {
		t.Errorf("%d", w.Code)
	}
}

func TestChipsCaptainReachesTheService(t *testing.T) {
	r := newRig(t, nil)
	r.do("GET", "/api/chips?captain=14", "", nil)
	if r.svc.chipsReq.Captain != 14 {
		t.Errorf("request = %+v", r.svc.chipsReq)
	}
	if w := r.do("GET", "/api/chips?captain=x", "", nil); w.Code != 400 {
		t.Errorf("%d", w.Code)
	}
}

// refresher is a NewsRefresher that records starts.
type refresher struct {
	starts  int
	running bool
}

func (f *refresher) Start(context.Context) (bool, error) {
	f.starts++
	was := f.running
	f.running = true
	return !was, nil
}

func (f *refresher) Status(context.Context) (news.RefreshStatus, error) {
	return news.RefreshStatus{Available: true, Running: f.running, LastNew: 4}, nil
}

func TestNewsRefresh(t *testing.T) {
	f := &refresher{}
	r := newRig(t, func(c *api.Config, _ *rig) { c.News = f })

	w := r.do("POST", "/api/news/refresh", "{}", nil)
	if w.Code != 202 || f.starts != 1 || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatalf("start: %d %s (starts %d)", w.Code, w.Body, f.starts)
	}
	again := r.do("POST", "/api/news/refresh", "{}", nil)
	if again.Code != 202 || !strings.Contains(again.Body.String(), `"running":true`) {
		t.Errorf("a second start while running is the same answer: %d %s", again.Code, again.Body)
	}
	got := r.do("GET", "/api/news/refresh", "", nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"last_new":4`) {
		t.Errorf("status: %d %s", got.Code, got.Body)
	}
}

func TestNewsRefreshIsOffWithoutARefresher(t *testing.T) {
	r := newRig(t, nil)

	if w := r.do("GET", "/api/news/refresh", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Errorf("status: %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/news/refresh", "{}", nil); w.Code != 503 {
		t.Errorf("start: %d %s, want 503", w.Code, w.Body)
	}
}

func TestNewsRefreshRefusesAForeignOrigin(t *testing.T) {
	f := &refresher{}
	r := newRig(t, func(c *api.Config, _ *rig) { c.News = f })

	w := r.do("POST", "/api/news/refresh", "{}", map[string]string{"Origin": "https://evil.example"})

	if w.Code != 403 || f.starts != 0 {
		t.Errorf("%d, starts %d: a page on another site must not be able to spend embedding credits", w.Code, f.starts)
	}
}

// inbox is an Inbox that records what it was asked.
type inbox struct {
	readTo  int64
	watched map[int]bool
}

func (i *inbox) Alerts(context.Context) (service.AlertsView, error) {
	return service.AlertsView{Alerts: []service.AlertView{{ID: 3, Title: "Palmer: suspended"}}, Unread: 1, Live: true}, nil
}
func (i *inbox) MarkRead(_ context.Context, upTo int64) error {
	if upTo < 1 {
		return &service.InputError{Msg: "up_to must be an alert id"}
	}
	i.readTo = upTo
	return nil
}
func (i *inbox) Watchlist(context.Context) (service.WatchlistView, error) {
	v := service.WatchlistView{}
	for id, on := range i.watched {
		if on {
			v.Players = append(v.Players, service.WatchedView{ID: id})
		}
	}
	return v, nil
}
func (i *inbox) SetWatch(ctx context.Context, id int, on bool) (service.WatchlistView, error) {
	i.watched[id] = on
	return i.Watchlist(ctx)
}
func (i *inbox) Health(context.Context) (service.HealthView, error) {
	return service.HealthView{Evals: []service.EvalRunView{{PassRate: 0.96}}}, nil
}

func TestInboxEndpoints(t *testing.T) {
	in := &inbox{watched: map[int]bool{}}
	r := newRig(t, func(c *api.Config, _ *rig) { c.Inbox = in })

	tests := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/api/alerts", "", 200, `"unread":1`},
		{"POST", "/api/alerts/read", `{"up_to": 3}`, 200, `"ok":true`},
		{"POST", "/api/alerts/read", `{"up_to": 0}`, 400, "alert id"},
		{"POST", "/api/alerts/read", `{"upto": 3}`, 400, "up_to"},
		{"POST", "/api/watchlist", `{"player_id": 4, "watch": true}`, 200, `"id":4`},
		{"GET", "/api/watchlist", "", 200, `"id":4`},
		{"POST", "/api/watchlist", `{"player_id": 4, "watch": false}`, 200, `"players":[]`},
		{"GET", "/api/health/runs", "", 200, `"pass_rate":0.96`},
	}
	for _, tt := range tests {
		w := r.do(tt.method, tt.path, tt.body, nil)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.contains) {
			t.Errorf("%s %s %s: %d %s", tt.method, tt.path, tt.body, w.Code, w.Body)
		}
	}
	if in.readTo != 3 {
		t.Errorf("read up to %d", in.readTo)
	}
	if w := r.do("POST", "/api/watchlist", `{"player_id": 4, "watch": true}`, map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("another site must not change the watchlist: %d", w.Code)
	}
}

func TestInboxEndpointsAreOffWithoutAnInbox(t *testing.T) {
	r := newRig(t, nil)
	for _, p := range []string{"/api/alerts", "/api/watchlist", "/api/health/runs"} {
		if w := r.do("GET", p, "", nil); w.Code != 503 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestGuard_AnswersNamedHostsOnlyWhenConfigured(t *testing.T) {
	tests := []struct {
		name   string
		hosts  []string
		host   string
		origin string
		status int
	}{
		{"named host behind the proxy", []string{"gaffer.1-2-3-4.sslip.io", "gaffer-api"}, "gaffer.1-2-3-4.sslip.io", "https://gaffer.1-2-3-4.sslip.io", 200},
		{"case and port do not matter", []string{"gaffer-api"}, "GAFFER-API:8080", "", 200},
		{"loopback still works", []string{"gaffer-api"}, "localhost:8080", "", 200},
		{"an unnamed host is refused", []string{"gaffer-api"}, "evil.example", "", 403},
		{"no names configured: as before", nil, "gaffer-api:8080", "", 403},
		{"a named host with a foreign origin", []string{"gaffer-api"}, "gaffer-api", "https://evil.example", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(c *api.Config, _ *rig) {
				c.AllowedHosts = tt.hosts
				c.AllowedOrigins = append(c.AllowedOrigins, "https://gaffer.1-2-3-4.sslip.io")
			})
			hdr := map[string]string{}
			if tt.origin != "" {
				hdr["Origin"] = tt.origin
			}
			req := httptest.NewRequest("GET", "/api/healthz", nil)
			req.Host = tt.host
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			r.srv.Handler().ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Errorf("%d %s, want %d", w.Code, w.Body, tt.status)
			}
		})
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name   string
		ready  func(context.Context) error
		status int
		leak   string
	}{
		{"no check configured", nil, 200, ""},
		{"database answers", func(context.Context) error { return nil }, 200, ""},
		{"database down", func(context.Context) error {
			return errors.New("dial tcp 10.0.2.4:5432: password authentication failed")
		}, 503, "password"},
	}
	for _, tt := range tests {
		r := newRig(t, func(c *api.Config, _ *rig) { c.Ready = tt.ready })
		w := r.do("GET", "/api/readyz", "", nil)
		if w.Code != tt.status {
			t.Errorf("%s: %d %s", tt.name, w.Code, w.Body)
		}
		if tt.leak != "" && strings.Contains(w.Body.String(), tt.leak) {
			t.Errorf("%s: the reason leaked: %s", tt.name, w.Body)
		}
	}
}

// settings is a SettingsService with one saved viewer per user.
type settings struct {
	saved   map[string]service.Viewer
	updates []string // users who saved something
	off     bool
}

func (f *settings) Get(_ context.Context, user string) (service.UserSettingsView, error) {
	if f.off {
		return service.UserSettingsView{}, service.ErrSettingsUnavailable
	}
	if user == "" {
		return service.UserSettingsView{}, &service.InputError{Msg: "settings need a signed-in user"}
	}
	v := f.saved[user]
	return service.UserSettingsView{User: user, TeamID: v.TeamID, Token: service.SavedTokenView{Saved: v.Token != ""}}, nil
}
func (f *settings) Update(ctx context.Context, user string, req service.UserSettingsRequest) (service.UserSettingsView, error) {
	f.updates = append(f.updates, user)
	return f.Get(ctx, user)
}
func (f *settings) Viewer(_ context.Context, user string) (service.Viewer, error) {
	if f.off {
		return service.Viewer{}, service.ErrSettingsUnavailable
	}
	return f.saved[user], nil
}

func TestSavedSettingsChooseTheSquad(t *testing.T) {
	const savedToken = "Bearer saved.SECRET.token"
	tests := []struct {
		name    string
		headers []string // user headers configured: behind the proxy
		hdr     map[string]string
		want    service.Viewer
	}{
		{"local user, nothing sent: the saved team and token", nil, nil, service.Viewer{TeamID: 111, Token: savedToken}},
		{"signed in by email", []string{"X-Auth-Request-Email", "X-Auth-Request-User"},
			map[string]string{"X-Auth-Request-Email": "Alice@Example.com", "X-Auth-Request-User": "alice"}, service.Viewer{TeamID: 222, Token: savedToken}},
		{"signed in, no email: the username", []string{"X-Auth-Request-Email", "X-Auth-Request-User"},
			map[string]string{"X-Auth-Request-User": "bob"}, service.Viewer{TeamID: 333}},
		{"the saved team named: its saved token is added", nil, map[string]string{"X-Gaffer-Team": "111"}, service.Viewer{TeamID: 111, Token: savedToken}},
		{"another team named: the saved token is never applied to it", nil, map[string]string{"X-Gaffer-Team": "999"}, service.Viewer{TeamID: 999}},
		{"a token sent: it is used as is", nil, map[string]string{"X-Gaffer-Team": "999", "X-Gaffer-Token": "Bearer a.b.c"}, service.Viewer{TeamID: 999, Token: "Bearer a.b.c"}},
		{"behind the proxy with no user: nothing saved applies", []string{"X-Auth-Request-User"}, nil, service.Viewer{}},
		{"a user header without a proxy is ignored", nil, map[string]string{"X-Auth-Request-User": "bob"}, service.Viewer{TeamID: 111, Token: savedToken}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &settings{saved: map[string]service.Viewer{
				"local":             {TeamID: 111, Token: savedToken},
				"alice@example.com": {TeamID: 222, Token: savedToken},
				"bob":               {TeamID: 333},
			}}
			r := newRig(t, func(c *api.Config, _ *rig) { c.Settings, c.UserHeaders = f, tt.headers })

			if w := r.do("GET", "/api/team", "", tt.hdr); w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if r.svc.viewer != tt.want {
				t.Errorf("viewer = %+v, want %+v", r.svc.viewer, tt.want)
			}
		})
	}
}

func TestSettingsEndpoints(t *testing.T) {
	f := &settings{saved: map[string]service.Viewer{"alice": {TeamID: 42, Token: "Bearer x.SECRET.y"}}}
	r := newRig(t, func(c *api.Config, _ *rig) { c.Settings, c.UserHeaders = f, []string{"X-Auth-Request-User"} })
	as := map[string]string{"X-Auth-Request-User": "alice"}

	w := r.do("GET", "/api/settings", "", as)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"team_id":42`) || strings.Contains(w.Body.String(), "SECRET") {
		t.Errorf("get: %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/settings", `{"team_id": 7}`, as); w.Code != 200 || len(f.updates) != 1 || f.updates[0] != "alice" {
		t.Errorf("post: %d %s %v", w.Code, w.Body, f.updates)
	}
	if w := r.do("POST", "/api/settings", `{"team": 7}`, as); w.Code != 400 {
		t.Errorf("unknown field: %d", w.Code)
	}
	if w := r.do("GET", "/api/settings", "", nil); w.Code != 400 {
		t.Errorf("behind the proxy without a user: %d %s", w.Code, w.Body)
	}

	off := newRig(t, func(c *api.Config, _ *rig) { c.Settings = &settings{off: true} })
	if w := off.do("GET", "/api/settings", "", nil); w.Code != 503 {
		t.Errorf("without a key: %d", w.Code)
	}
	if w := off.do("GET", "/api/team", "", nil); w.Code != 200 {
		t.Errorf("settings being off must not break the rest: %d", w.Code)
	}
}

func TestSubsEndpoint(t *testing.T) {
	r := newRig(t, nil)

	w := r.do("POST", "/api/whatif/subs", `{"swaps": [{"out": 110, "in": 115}]}`, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"formation":"3-5-2"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(r.svc.subsReq.Swaps) != 1 || r.svc.subsReq.Swaps[0] != (service.SubsSwapRequest{Out: 110, In: 115}) {
		t.Errorf("request reached the service as %+v", r.svc.subsReq)
	}
	if w := r.do("POST", "/api/whatif/subs", `{"subs": []}`, nil); w.Code != 400 {
		t.Errorf("unknown field: %d", w.Code)
	}
	if w := r.do("GET", "/api/whatif/subs", "", nil); w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
}
