package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TakeUsage counts one use of kind by user on day, unless that would go over limit. It reports the
// count after this use and whether it was allowed. The check and the count are one statement, so
// two requests at once cannot both slip under the limit.
func (s *Store) TakeUsage(ctx context.Context, user, kind string, day time.Time, limit int) (int, bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `INSERT INTO usage_counts (user_id, day, kind, n) VALUES ($1, $2, $3, 1)
		ON CONFLICT (user_id, day, kind) DO UPDATE SET n = usage_counts.n + 1 WHERE usage_counts.n < $4
		RETURNING n`, user, day.UTC().Format(time.DateOnly), kind, limit).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return limit, false, nil // the update's WHERE refused: already at the limit
	}
	if err != nil {
		return 0, false, fmt.Errorf("count %s use: %w", kind, err)
	}
	return n, true, nil
}

// UsageOn returns how many times user used each kind on day.
func (s *Store) UsageOn(ctx context.Context, user string, day time.Time) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT kind, n FROM usage_counts WHERE user_id = $1 AND day = $2`, user, day.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("read usage: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, fmt.Errorf("scan usage: %w", err)
		}
		out[kind] = n
	}
	return out, rows.Err()
}
