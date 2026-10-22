package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/config"
	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/ingest"
)

const usage = "usage: gaffer sync | gaffer news | gaffer squad    (env: GAFFER_DSN required, GAFFER_MIGRATIONS default ./migrations)"

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
	// Optional local config; real environment variables take precedence.
	if err := config.LoadDotEnv(".env"); err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	if len(args) != 1 {
		return errors.New(usage)
	}
	switch args[0] {
	case "sync":
		return runSync(ctx)
	case "news":
		return runNews(ctx)
	case "squad":
		return runSquad(ctx)
	}
	return errors.New(usage)
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

	client := fpl.NewHTTPClient(politeClient(100 * time.Millisecond))

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
