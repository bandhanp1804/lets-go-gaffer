// Package api serves the assistant's read-only questions over HTTP as JSON, and the
// assistant itself as a server-sent-event stream. It shares one service layer with
// the CLI, so the two cannot disagree.
//
// The server has no login: it is meant for the manager's own machine. Because a
// browser will happily call localhost on behalf of any web page the manager visits,
// guard.go refuses requests that did not come from the UI.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// Service is what the endpoints read. *service.Service satisfies it.
type Service interface {
	MyTeam(ctx context.Context) (service.MyTeamView, error)
	Lineup(ctx context.Context) (service.LineupView, error)
	Transfers(ctx context.Context, req service.TransfersRequest) (service.TransfersView, error)
	FindPlayers(ctx context.Context, q service.PlayerQuery) ([]service.PlayerView, error)
	SearchNews(ctx context.Context, q service.NewsQuery) ([]service.NewsView, error)
	Status(ctx context.Context) (service.StatusView, error)
	BuildSquad(ctx context.Context, req service.SquadRequest) (service.SquadView, error)
	Backtest(ctx context.Context) (service.BacktestView, error)
}

// Conversation is one assistant conversation. *assistant.Session satisfies it.
type Conversation interface {
	Ask(ctx context.Context, question string, onText func(string)) (assistant.Answer, error)
}

// Config configures the server. Only Service is required.
type Config struct {
	Service         Service
	NewConversation func() Conversation // nil turns /api/ask off
	AllowedOrigins  []string            // origins of the UI, e.g. "http://localhost:3001"
	Logger          *slog.Logger        // default slog.Default()

	MaxSessions int           // conversations kept in memory; default 100
	SessionIdle time.Duration // idle time before a conversation is forgotten; default 30m
	ReadTimeout time.Duration // limit for one JSON endpoint; default 90s (a news search may wait on a rate limiter)
	AskTimeout  time.Duration // limit for one question; default 3m
}

// maxQuestion bounds a question, so a request cannot make the model read a novel.
const maxQuestion = 2000

// Server is the HTTP handler plus the conversations it holds.
type Server struct {
	cfg      Config
	log      *slog.Logger
	sessions *sessions
	handler  http.Handler
}

// New builds the server. The background goroutine that forgets idle conversations
// stops when ctx is cancelled.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Service == nil {
		return nil, errors.New("api: a Service is required")
	}
	for _, o := range cfg.AllowedOrigins {
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") || strings.HasSuffix(o, "/") {
			return nil, errors.New("api: allowed origin " + o + " must look like http://localhost:3001 (scheme, host, port, no trailing slash)")
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.MaxSessions = orDefault(cfg.MaxSessions, 100)
	cfg.SessionIdle = orDefault(cfg.SessionIdle, 30*time.Minute)
	cfg.ReadTimeout = orDefault(cfg.ReadTimeout, 90*time.Second)
	cfg.AskTimeout = orDefault(cfg.AskTimeout, 3*time.Minute)

	s := &Server{cfg: cfg, log: cfg.Logger, sessions: newSessions(cfg.MaxSessions, cfg.SessionIdle, time.Now)}
	go s.sessions.janitor(ctx, cfg.SessionIdle/2)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/status", s.read(func(ctx context.Context, r *http.Request) (any, error) { return cfg.Service.Status(ctx) }))
	mux.HandleFunc("GET /api/squad", s.read(s.squad))
	mux.HandleFunc("GET /api/backtest", s.read(func(ctx context.Context, r *http.Request) (any, error) { return cfg.Service.Backtest(ctx) }))
	mux.HandleFunc("GET /api/team", s.read(func(ctx context.Context, r *http.Request) (any, error) { return cfg.Service.MyTeam(ctx) }))
	mux.HandleFunc("GET /api/lineup", s.read(func(ctx context.Context, r *http.Request) (any, error) { return cfg.Service.Lineup(ctx) }))
	mux.HandleFunc("GET /api/transfers", s.read(s.transfers))
	mux.HandleFunc("GET /api/players", s.read(s.players))
	mux.HandleFunc("GET /api/news", s.read(s.news))
	mux.HandleFunc("POST /api/ask", s.ask)

	s.handler = recoverer(s.log, logRequests(s.log, guard(cfg.AllowedOrigins, mux)))
	return s, nil
}

// Handler is the root handler to serve.
func (s *Server) Handler() http.Handler { return s.handler }

func orDefault[T int | time.Duration](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}
