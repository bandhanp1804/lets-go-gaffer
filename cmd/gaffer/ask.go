package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/config"
	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"

	"github.com/bandhanpatel/lets-go-gaffer/internal/telemetry"
)

const askUsage = `usage: gaffer ask [-v] QUESTION
       gaffer chat [-v]
  -v   also show which tools were called and how many tokens were used`

// parseVerbose reads the -v flag and returns the remaining arguments.
func parseVerbose(name string, args []string) (verbose bool, rest []string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&verbose, "v", false, "")
	if err := fs.Parse(args); err != nil {
		return false, nil, fmt.Errorf("%w\n%s", err, askUsage)
	}
	return verbose, fs.Args(), nil
}

// parseAskArgs reads the flags and joins the rest into the question.
func parseAskArgs(args []string) (question string, verbose bool, err error) {
	verbose, rest, err := parseVerbose("ask", args)
	if err != nil {
		return "", false, err
	}
	question = strings.TrimSpace(strings.Join(rest, " "))
	if question == "" {
		return "", verbose, errors.New(askUsage)
	}
	return question, verbose, nil
}

// newAnthropic is the Claude client, with retries for rate limits and server errors.
func newAnthropic(key string) (*llm.Anthropic, error) {
	return llm.NewAnthropic(&http.Client{Transport: httpx.NewRetryTransport(telemetry.Transport(nil), httpx.RetryConfig{
		MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 15 * time.Second,
	})}, llm.Config{APIKey: key})
}

// buildService wires the database, FPL, the optional live session, and the embedder into
// the service the CLI, the assistant and the API share. The caller calls the returned
// function when done.
func buildService(ctx context.Context) (*service.Service, func(), error) {
	st, closeFn, err := buildStack(ctx)
	if err != nil {
		return nil, nil, err
	}
	return st.svc, closeFn, nil
}

// stack is the service plus the pieces `gaffer serve` shares with its news refresh, so a refresh
// and a search use one database pool and one embedding rate limit.
type stack struct {
	svc      *service.Service
	store    *db.Store
	embedder *embed.Voyage // nil without VOYAGE_API_KEY
}

func buildStack(ctx context.Context) (stack, func(), error) {
	dsn := os.Getenv("GAFFER_DSN")
	if dsn == "" {
		return stack{}, nil, errors.New("GAFFER_DSN is not set")
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return stack{}, nil, fmt.Errorf("open database: %w", err)
	}
	// The server and the worker read tables a later version added (alerts, watchlist), so a
	// database that was only ever synced by an older build is brought up to date here.
	migrations := os.Getenv("GAFFER_MIGRATIONS")
	if migrations == "" {
		migrations = "migrations"
	}
	if err := db.Migrate(ctx, pool, os.DirFS(migrations)); err != nil {
		pool.Close()
		return stack{}, nil, fmt.Errorf("migrate: %w", err)
	}

	// The token lasts an hour and this process may run for days (gaffer serve), so it is
	// read again whenever .env changes instead of once at startup.
	token := config.NewLiveVar(".env", sessionEnv)
	store := db.NewStore(pool)
	deps := service.Deps{
		Store:     store,
		FPL:       fpl.NewHTTPClient(politeClient(100 * time.Millisecond)),
		SessionFn: token.Get,
		Live:      fpl.NewHTTPClient(sessionClientFunc(token.Get)), // sends nothing while the token is empty
	}
	// News search needs Voyage. Without a key the assistant still works; that tool reports it is unavailable.
	var embedder *embed.Voyage
	if os.Getenv("VOYAGE_API_KEY") != "" {
		var err error
		if embedder, err = newEmbedder(); err != nil {
			pool.Close()
			return stack{}, nil, err
		}
		deps.Embedder = embedder
	}

	teamID := 0
	fmt.Sscanf(strings.TrimSpace(os.Getenv("GAFFER_TEAM_ID")), "%d", &teamID)
	stopPool, err := telemetry.ObservePool(func() telemetry.PoolStats {
		s := pool.Stat()
		return telemetry.PoolStats{Acquired: s.AcquiredConns(), Idle: s.IdleConns(), Max: s.MaxConns(), EmptyAcquires: s.EmptyAcquireCount()}
	})
	if err != nil {
		pool.Close()
		return stack{}, nil, fmt.Errorf("observe the database pool: %w", err)
	}
	closeAll := func() { stopPool(); pool.Close() }
	return stack{svc: service.New(deps, service.Config{TeamID: teamID}), store: store, embedder: embedder}, closeAll, nil
}

// buildAssistant is buildService plus Claude. The caller calls the returned function when done.
func buildAssistant(ctx context.Context) (*assistant.Assistant, func(), error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, nil, errors.New("ANTHROPIC_API_KEY is not set (add it to .env)")
	}
	svc, closeFn, err := buildService(ctx)
	if err != nil {
		return nil, nil, err
	}
	model, err := newAnthropic(key)
	if err != nil {
		closeFn()
		return nil, nil, err
	}
	return assistant.New(model, svc, assistant.Config{Model: llm.ModelFromEnv(), Clubs: clubLegend(ctx, svc)}), closeFn, nil
}

// clubLegend is this season's club codes and names, which the assistant is given so it never
// has to remember them. If the season's data cannot be read now the assistant still works,
// without the legend and without the check that depends on it.
func clubLegend(ctx context.Context, svc *service.Service) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := svc.Status(ctx)
	if err != nil {
		slog.Warn("could not read the club list; the assistant will run without it", "err", err)
		return nil
	}
	clubs := make(map[string]string, len(st.Clubs))
	for _, c := range st.Clubs {
		clubs[c.Short] = c.Name
	}
	return clubs
}

// runAsk answers one question, streaming the answer as it is written.
func runAsk(ctx context.Context, args []string) error {
	question, verbose, err := parseAskArgs(args)
	if err != nil {
		return err
	}
	a, closeFn, err := buildAssistant(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	return askOnce(ctx, a.NewSession(), question, verbose)
}

// runChat is a conversation: follow-up questions see earlier answers and keep their citations.
func runChat(ctx context.Context, args []string) error {
	verbose, rest, err := parseVerbose("chat", args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New(askUsage)
	}
	a, closeFn, err := buildAssistant(ctx)
	if err != nil {
		return err
	}
	defer closeFn()

	session := a.NewSession()
	fmt.Println("Ask about your squad, lineup, transfers, players, or the news. Blank line or Ctrl-D to quit.")
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			fmt.Println()
			return nil
		}
		q := strings.TrimSpace(in.Text())
		if q == "" || ctx.Err() != nil {
			return nil
		}
		if err := askOnce(ctx, session, q, verbose); err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err) // a failed turn leaves the conversation intact
		}
	}
}

func askOnce(ctx context.Context, s *assistant.Session, question string, verbose bool) error {
	ans, err := s.Ask(ctx, question, func(p string) { fmt.Print(p) })
	if err != nil {
		return err
	}
	fmt.Println()
	printAnswerExtras(os.Stdout, ans, verbose)
	return nil
}

// printAnswerExtras prints what follows the streamed answer: the stories it cites,
// anything the checks found wrong, and with verbose the tools used and tokens spent.
func printAnswerExtras(w io.Writer, ans assistant.Answer, verbose bool) {
	if len(ans.Sources) > 0 {
		fmt.Fprintln(w, "\nSources")
		for _, s := range ans.Sources {
			where := s.URL
			if strings.HasPrefix(where, "fpl://") {
				where = "(official FPL player note, no web page)" // an internal identifier, not a link
			}
			fmt.Fprintf(w, "  [%d] %s (%s, %s ago)\n      %s\n", s.Ref, s.Title, s.Source, s.Age, where)
		}
	}
	if warnings := ans.Warnings(); len(warnings) > 0 {
		fmt.Fprintln(w, "\nChecks: verify these before relying on the answer")
		for _, msg := range warnings {
			fmt.Fprintf(w, "  ! %s\n", msg)
		}
	}
	if verbose {
		fmt.Fprintf(w, "\nTools (%d model calls)\n", ans.Rounds)
		for _, c := range ans.ToolCalls {
			status := "ok"
			if c.Error != "" {
				status = "error: " + c.Error
			}
			fmt.Fprintf(w, "  %s %s  %s  %s\n", c.Name, c.Input, c.Duration.Round(time.Millisecond), status)
		}
		u := ans.Usage
		fmt.Fprintf(w, "Tokens: %d in (%d read from cache), %d out\n", u.InputTokens, u.CacheReadTokens, u.OutputTokens)
	}
}
