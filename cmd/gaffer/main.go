package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
)

const usage = "usage: gaffer sync    (env: GAFFER_DSN required, GAFFER_MIGRATIONS default ./migrations)"

func main() {
	// Ctrl-C / SIGTERM cancels ctx; workers and the pool unwind before we exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.Error("gaffer failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		return errors.New(usage)
	}
	return runSync(ctx)
}

// runSync pulls the FPL season into Postgres.
func runSync(ctx context.Context) error {
	dsn := os.Getenv("GAFFER_DSN")
	if dsn == "" {
		return errors.New("GAFFER_DSN is not set")
	}
	migrations := os.Getenv("GAFFER_MIGRATIONS")
	if migrations == "" {
		migrations = "migrations"
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, os.DirFS(migrations)); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Retry wraps the rate limiter so every attempt, including retries, is paced.
	// No http.Client.Timeout: it would cut off backoff; ctx bounds the whole run.
	transport := fpl.NewRetryTransport(
		fpl.NewRateLimitTransport(http.DefaultTransport, fpl.NewLimiter(100*time.Millisecond)),
		fpl.RetryConfig{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second},
	)
	client := fpl.NewHTTPClient(&http.Client{Transport: transport})

	start := time.Now()
	sum, err := ingest.Sync(ctx, client, db.NewStore(pool), ingest.Config{Workers: 8, MaxFailures: 20})

	slog.Info("sync finished",
		"players_done", sum.Done, "history_rows", sum.Rows,
		"failed", len(sum.Failed), "failed_ids", sum.FailedIDs(),
		"elapsed", time.Since(start).Round(time.Millisecond))
	for _, id := range sum.FailedIDs() {
		slog.Warn("player failed", "err", sum.Failed[id])
	}
	return err
}
