package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserSettings is one user's stored row. The team and token are ciphertexts; the store never
// sees them in the clear. TokenExpires is zero when no token is saved.
type UserSettings struct {
	TeamCT       []byte
	TokenCT      []byte
	TokenExpires time.Time
	UpdatedAt    time.Time
}

// UserSettings returns a user's row, and false if they have none.
func (s *Store) UserSettings(ctx context.Context, user string) (UserSettings, bool, error) {
	var u UserSettings
	var exp *time.Time
	err := s.pool.QueryRow(ctx, `SELECT team_ct, token_ct, token_expires, updated_at FROM user_settings WHERE user_id = $1`, user).
		Scan(&u.TeamCT, &u.TokenCT, &exp, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSettings{}, false, nil
	}
	if err != nil {
		return UserSettings{}, false, fmt.Errorf("read settings: %w", err)
	}
	if exp != nil {
		u.TokenExpires = *exp
	}
	return u, true, nil
}

// SaveUserTeam stores a user's encrypted team, keeping their token.
func (s *Store) SaveUserTeam(ctx context.Context, user string, teamCT []byte) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO user_settings (user_id, team_ct) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET team_ct = EXCLUDED.team_ct, updated_at = now()`, user, teamCT)
	if err != nil {
		return fmt.Errorf("save team: %w", err)
	}
	return nil
}

// SaveUserToken stores a user's encrypted token and when it expires, keeping their team. A nil
// token clears it.
func (s *Store) SaveUserToken(ctx context.Context, user string, tokenCT []byte, expires time.Time) error {
	var exp *time.Time
	if tokenCT != nil && !expires.IsZero() {
		exp = &expires
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO user_settings (user_id, token_ct, token_expires) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET token_ct = EXCLUDED.token_ct, token_expires = EXCLUDED.token_expires, updated_at = now()`,
		user, tokenCT, exp)
	if err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	return nil
}
