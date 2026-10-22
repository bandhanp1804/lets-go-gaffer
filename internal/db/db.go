// Package db is the Postgres + pgvector storage layer.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Open connects a pool and registers the pgvector types on every connection it
// creates. Registration happens per connection (AfterConnect), not once, because
// each pooled connection has its own type map.
//
// On a database that has not been migrated yet the vector type does not exist and
// registration is skipped; Migrate resets the pool afterwards so that connections
// opened before the extension existed are replaced.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var has bool
		if err := conn.QueryRow(ctx, `SELECT to_regtype('vector') IS NOT NULL`).Scan(&has); err != nil {
			return fmt.Errorf("check vector type: %w", err)
		}
		if !has {
			return nil
		}
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			return fmt.Errorf("register vector types: %w", err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
