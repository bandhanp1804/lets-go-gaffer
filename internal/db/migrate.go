package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockKey is an arbitrary constant for pg_advisory_xact_lock, so two
// processes starting at once (e.g. several replicas) do not apply migrations twice.
const migrationLockKey = 726_001

// Migrate applies every *.up.sql file in fsys, in filename order, that has not
// been applied yet. Each migration runs in its own transaction together with its
// bookkeeping row, so a failure leaves nothing half-applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	// Under the same lock as the migrations: two replicas creating the table at the same moment
	// can otherwise both fail on the catalog, IF NOT EXISTS notwithstanding.
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)

	applied := false
	for _, name := range files {
		version := strings.TrimSuffix(name, ".up.sql")
		ran, err := applyOne(ctx, pool, fsys, name, version)
		if err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
		applied = applied || ran
	}

	if applied {
		// Connections opened before CREATE EXTENSION ran never registered the
		// vector type. Closing idle ones makes the pool reconnect with it.
		pool.Reset()
	}
	return nil
}

func applyOne(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, name, version string) (bool, error) {
	sqlText, err := fs.ReadFile(fsys, name)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return false, fmt.Errorf("lock: %w", err)
	}

	var done bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done)
	if err != nil {
		return false, fmt.Errorf("check applied: %w", err)
	}
	if done {
		return false, nil
	}

	// No arguments, so pgx sends this as a simple query and multiple statements work.
	if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
		return false, fmt.Errorf("apply: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return false, fmt.Errorf("record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
