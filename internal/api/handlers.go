package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// read wraps a JSON endpoint: a deadline, error mapping, and JSON output.
func (s *Server) read(f func(ctx context.Context, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
		defer cancel()
		v, err := f(ctx, r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) transfers(ctx context.Context, r *http.Request) (any, error) {
	q := r.URL.Query()
	req := service.TransfersRequest{}
	var err error
	if req.Wildcard, err = boolParam(q.Get("wildcard")); err != nil {
		return nil, badParam("wildcard", err)
	}
	if v := q.Get("free_transfers"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, badParam("free_transfers", errors.New("must be a whole number, zero or more"))
		}
		req.Free = &n
	}
	if req.Horizon, err = intParam(q.Get("horizon")); err != nil {
		return nil, badParam("horizon", err)
	}
	if req.Max, err = intParam(q.Get("max_transfers")); err != nil {
		return nil, badParam("max_transfers", err)
	}
	return s.cfg.Service.Transfers(ctx, req)
}

func (s *Server) lineup(ctx context.Context, r *http.Request) (any, error) {
	q := r.URL.Query()
	req := service.LineupRequest{Formation: q.Get("formation")}
	var err error
	if req.Captain, err = intParam(q.Get("captain")); err != nil {
		return nil, badParam("captain", err)
	}
	if req.Vice, err = intParam(q.Get("vice")); err != nil {
		return nil, badParam("vice", err)
	}
	return s.cfg.Service.LineupWith(ctx, req)
}

func (s *Server) chips(ctx context.Context, r *http.Request) (any, error) {
	captain, err := intParam(r.URL.Query().Get("captain"))
	if err != nil {
		return nil, badParam("captain", err)
	}
	return s.cfg.Service.ChipsFor(ctx, service.ChipsRequest{Captain: captain})
}

func (s *Server) squad(ctx context.Context, r *http.Request) (any, error) {
	horizon, err := intParam(r.URL.Query().Get("horizon"))
	if err != nil {
		return nil, badParam("horizon", err)
	}
	return s.cfg.Service.BuildSquad(ctx, service.SquadRequest{Horizon: horizon})
}

func (s *Server) players(ctx context.Context, r *http.Request) (any, error) {
	q := r.URL.Query()
	pq := service.PlayerQuery{Position: q.Get("position"), Team: q.Get("team"), Name: q.Get("name"), Status: q.Get("status"), SortBy: q.Get("sort_by"), SortDir: q.Get("sort_dir")}
	var err error
	if pq.MaxPrice, err = floatParam(q.Get("max_price")); err != nil {
		return nil, badParam("max_price", err)
	}
	if pq.MinPrice, err = floatParam(q.Get("min_price")); err != nil {
		return nil, badParam("min_price", err)
	}
	if pq.Limit, err = intParam(q.Get("limit")); err != nil {
		return nil, badParam("limit", err)
	}
	if pq.Offset, err = intParam(q.Get("offset")); err != nil {
		return nil, badParam("offset", err)
	}
	return s.cfg.Service.Players(ctx, pq)
}

func (s *Server) news(ctx context.Context, r *http.Request) (any, error) {
	q := r.URL.Query()
	nq := service.NewsQuery{Query: q.Get("q"), Player: q.Get("player"), NoDetect: q.Get("detect") == "off"}
	var err error
	if nq.Days, err = intParam(q.Get("days")); err != nil {
		return nil, badParam("days", err)
	}
	if nq.Limit, err = intParam(q.Get("limit")); err != nil {
		return nil, badParam("limit", err)
	}
	return s.cfg.Service.Search(ctx, nq)
}

func badParam(name string, err error) error {
	return &service.InputError{Msg: fmt.Sprintf("query parameter %s: %v", name, err)}
}

func boolParam(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, errors.New("must be true or false")
	}
	return b, nil
}

func intParam(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New("must be a whole number")
	}
	return n, nil
}

func floatParam(v string) (float64, error) {
	if v == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, errors.New("must be a number")
	}
	return f, nil
}

// whatIf weighs a transfer plan. It is a POST because the plan is a body, not because it
// changes anything: nothing is stored and nothing is sent to FPL.
// newsRefreshStatus says whether a refresh is running and how the last one went.
func (s *Server) newsRefreshStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.News == nil {
		writeJSON(w, http.StatusOK, news.RefreshStatus{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	st, err := s.cfg.News.Status(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// newsRefresh starts a refresh and answers at once: pulling and embedding can take a minute or
// more on a rate-limited key, so the page polls newsRefreshStatus instead of waiting on this. A
// refresh already running is not an error; the answer is the same ("running").
func (s *Server) newsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.News == nil {
		s.fail(w, r, service.ErrNewsUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	if _, err := s.cfg.News.Start(ctx); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.cfg.News.Status(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st.Running = true // it was just started, or was already going; a list may not show it yet
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) settings(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Settings == nil {
		return nil, service.ErrSettingsUnavailable
	}
	return s.cfg.Settings.Get(ctx, userOf(ctx))
}

// updateSettings saves the user's team and token: {"team_id": 1234567, "token": "...",
// "clear_token": false}. The answer never contains the token.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Settings == nil {
		s.fail(w, r, service.ErrSettingsUnavailable)
		return
	}
	var req service.UserSettingsRequest
	if !readBody(w, r, &req, `{"team_id": 1234567, "token": "Bearer ..."}`) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	v, err := s.cfg.Settings.Update(ctx, userOf(ctx), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// errInboxOff is returned by the inbox endpoints of a server built without one.
var errInboxOff = errors.New("alerts are not available on this server")

func (s *Server) alerts(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Inbox == nil {
		return nil, errInboxOff
	}
	return s.cfg.Inbox.Alerts(ctx)
}

func (s *Server) watchlist(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Inbox == nil {
		return nil, errInboxOff
	}
	return s.cfg.Inbox.Watchlist(ctx)
}

func (s *Server) health(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Inbox == nil {
		return nil, errInboxOff
	}
	return s.cfg.Inbox.Health(ctx)
}

// readBody decodes a small JSON body, refusing unknown fields, and answers 400 itself on failure.
func readBody(w http.ResponseWriter, r *http.Request, v any, example string) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "send a JSON body like "+example)
		return false
	}
	return true
}

// markRead marks the alerts up to an id as read: {"up_to": 12}.
func (s *Server) markRead(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == nil {
		s.fail(w, r, errInboxOff)
		return
	}
	var body struct {
		UpTo int64 `json:"up_to"`
	}
	if !readBody(w, r, &body, `{"up_to": 12}`) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	if err := s.cfg.Inbox.MarkRead(ctx, body.UpTo); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// setWatch watches a player or stops: {"player_id": 4, "watch": true}.
func (s *Server) setWatch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Inbox == nil {
		s.fail(w, r, errInboxOff)
		return
	}
	var body struct {
		PlayerID int  `json:"player_id"`
		Watch    bool `json:"watch"`
	}
	if !readBody(w, r, &body, `{"player_id": 4, "watch": true}`) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	v, err := s.cfg.Inbox.SetWatch(ctx, body.PlayerID, body.Watch)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) whatIf(w http.ResponseWriter, r *http.Request) {
	var req service.WhatIfRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "send a JSON body like {\"moves\": [{\"out\": 101, \"in\": 201}]}")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	v, err := s.cfg.Service.WhatIf(ctx, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// subs weighs substitutions: {"swaps": [{"out": 110, "in": 115}]}. Like whatIf it changes nothing.
func (s *Server) subs(w http.ResponseWriter, r *http.Request) {
	var req service.SubsRequest
	if !readBody(w, r, &req, `{"swaps": [{"out": 110, "in": 115}]}`) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()
	v, err := s.cfg.Service.Subs(ctx, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// askRequest is the body of POST /api/ask.
type askRequest struct {
	Question  string `json:"question"`
	SessionID string `json:"session_id"`
}

// AskTool is one tool call, as shown to the UI. Tool outputs are not sent.
type AskTool struct {
	Name       string `json:"name"`
	Input      string `json:"input"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// AskSource is a story the answer cites.
type AskSource struct {
	Ref    int    `json:"ref"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Source string `json:"source"`
	Age    string `json:"age"`
}

// AskUsage is what the question cost in tokens.
type AskUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
}

// AskDone is the final event of an answer stream.
type AskDone struct {
	SessionID string      `json:"session_id"`
	Text      string      `json:"text"`
	Sources   []AskSource `json:"sources"`
	Warnings  []string    `json:"warnings"`
	Tools     []AskTool   `json:"tools"`
	Rounds    int         `json:"rounds"`
	Usage     AskUsage    `json:"usage"`
}

// ask streams one answer as server-sent events:
//
//	event: session data: {"session_id":"..."} first, so a client can follow up even before the answer ends
//	event: text   data: {"delta":"..."}      as the answer is written
//	event: done   data: {...}                 once, with sources, warnings and usage
//	event: error  data: {"error":"..."}      if the turn fails after streaming began
//
// Problems found before streaming starts (a bad body, an unknown or busy
// conversation) are ordinary JSON errors with a status code.
func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	if s.cfg.NewConversation == nil {
		writeError(w, http.StatusServiceUnavailable, "the assistant is not configured (no ANTHROPIC_API_KEY)")
		return
	}
	var req askRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "send a JSON body like {\"question\": \"...\"}")
		return
	}
	if len(req.Question) == 0 || len(req.Question) > maxQuestion {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the question must be between 1 and %d characters", maxQuestion))
		return
	}
	id, conv, release, err := s.sessions.acquire(req.SessionID, owner(service.ViewerFrom(r.Context())), s.cfg.NewConversation)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errUnknownSession):
			status = http.StatusNotFound
		case errors.Is(err, errSessionBusy):
			status = http.StatusConflict
		case errors.Is(err, errTooManySession):
			status = http.StatusServiceUnavailable
		}
		if status == http.StatusInternalServerError {
			s.log.Error("start conversation", "err", err)
			err = errors.New("internal error")
		}
		writeError(w, status, err.Error())
		return
	}
	defer release()

	flusher := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // proxies must not buffer the stream
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex // the stream has one writer at a time
	send := func(event string, v any) {
		b, _ := json.Marshal(nonNil(v))
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		_ = flusher.Flush()
	}

	send("session", map[string]string{"session_id": id})

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.AskTimeout)
	defer cancel()
	ans, err := conv.Ask(ctx, req.Question, func(p string) { send("text", map[string]string{"delta": p}) })
	if err != nil {
		if ctx.Err() == nil { // not a disconnect or timeout: worth a log line
			s.log.Error("ask", "err", err)
		}
		send("error", map[string]string{"error": askError(ctx, err)})
		return
	}

	done := AskDone{
		SessionID: id, Text: ans.Text, Sources: []AskSource{}, Warnings: ans.Warnings(), Tools: []AskTool{}, Rounds: ans.Rounds,
		Usage: AskUsage{InputTokens: ans.Usage.InputTokens, OutputTokens: ans.Usage.OutputTokens, CacheReadTokens: ans.Usage.CacheReadTokens},
	}
	if done.Warnings == nil {
		done.Warnings = []string{}
	}
	for _, src := range ans.Sources {
		done.Sources = append(done.Sources, AskSource{Ref: src.Ref, Title: src.Title, URL: src.URL, Source: src.Source, Age: src.Age})
	}
	for _, c := range ans.ToolCalls {
		done.Tools = append(done.Tools, AskTool{Name: c.Name, Input: c.Input, Error: c.Error, DurationMS: c.Duration.Milliseconds()})
	}
	send("done", done)
}

// askError is the message for a failed turn: what the manager can act on, not internals.
func askError(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "the question took too long and was stopped"
	case ctx.Err() != nil:
		return "the request was cancelled"
	}
	return "the assistant could not answer; try again"
}

// fail maps an error to a status. Input errors and "unavailable" carry messages meant
// for the caller; anything else is logged and reported generically.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var in *service.InputError
	switch {
	case errors.Is(err, service.ErrTeamRequired):
		writeJSON(w, http.StatusBadRequest, ErrorBody{Error: err.Error(), Code: "team_required"})
	case errors.As(err, &in):
		writeError(w, http.StatusBadRequest, in.Msg)
	case errors.Is(err, service.ErrNewsUnavailable), errors.Is(err, errInboxOff), errors.Is(err, service.ErrSettingsUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "the request took too long")
	case errors.Is(err, context.Canceled):
		// the client went away; there is nobody to answer
	default:
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(nonNil(v)) // lists are never null: see nonNil
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// ErrorBody is the body of every error response. Code, when set, is for the client to act
// on (for example to ask for a team id) and does not change with the wording of Error.
type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorBody{Error: msg})
}
