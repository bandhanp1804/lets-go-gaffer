package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/secretbox"
)

const (
	maxTokenLen = 4096
	maxTeamID   = 999_999_999
)

// tokenBody is what a bearer token may contain: a JWT is base64url text and dots.
var tokenBody = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// NormalizeToken checks a pasted FPL token and returns it as "Bearer <token>". The error says
// what is wrong with it, never what it held.
func NormalizeToken(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxTokenLen {
		return "", &InputError{Msg: "the token is too long to be an FPL token"}
	}
	body := raw
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		body = strings.TrimSpace(raw[7:])
	}
	if body == "" || !tokenBody.MatchString(body) {
		return "", &InputError{Msg: "that does not look like an FPL token: paste only the value after \"Bearer\""}
	}
	return "Bearer " + body, nil
}

// CheckTeamID checks a team id is in FPL's range.
func CheckTeamID(id int) error {
	if id < 1 || id > maxTeamID {
		return &InputError{Msg: "the team id must be a number such as 1234567"}
	}
	return nil
}

// ErrSettingsUnavailable means the server has no encryption key, so it cannot keep settings.
var ErrSettingsUnavailable = errors.New("saved settings are off: the server has no GAFFER_DATA_KEY")

// SettingsStore is what Settings keeps rows in. *db.Store satisfies it.
type SettingsStore interface {
	UserSettings(ctx context.Context, user string) (db.UserSettings, bool, error)
	SaveUserTeam(ctx context.Context, user string, teamCT []byte) error
	SaveUserToken(ctx context.Context, user string, tokenCT []byte, expires time.Time) error
}

// Settings keeps each signed-in user's team and FPL token, encrypted, so any device they sign
// in on loads their team (and their live squad while the token lasts).
type Settings struct {
	Store SettingsStore
	Box   *secretbox.Box // nil: settings are off
	Now   func() time.Time
}

// SavedTokenView is what the Setup page shows about a saved token. The token itself is never sent back.
type SavedTokenView struct {
	Saved     bool   `json:"saved"`
	Expired   bool   `json:"expired"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC 3339
	ExpiresIn string `json:"expires_in,omitempty"` // "42 min", "1h"
}

// UserSettingsView is one user's settings.
type UserSettingsView struct {
	User   string         `json:"user"`
	TeamID int            `json:"team_id"` // 0: none saved
	Token  SavedTokenView `json:"token"`
}

// UserSettingsRequest changes some settings; absent fields are left alone.
type UserSettingsRequest struct {
	TeamID     *int    `json:"team_id,omitempty"`
	Token      *string `json:"token,omitempty"`
	ClearToken bool    `json:"clear_token,omitempty"`
	ClearTeam  bool    `json:"clear_team,omitempty"`
}

func (s *Settings) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// The context binds a ciphertext to its user and field, so it cannot be moved to another row.
func teamContext(user string) string  { return "team:" + user }
func tokenContext(user string) string { return "token:" + user }

// load decrypts a user's row. A value that will not decrypt (the key changed) reads as unset,
// so the user is asked again instead of being locked out.
func (s *Settings) load(ctx context.Context, user string) (team int, token string, expires time.Time, err error) {
	if s == nil || s.Box == nil {
		return 0, "", time.Time{}, ErrSettingsUnavailable
	}
	if user == "" {
		return 0, "", time.Time{}, &InputError{Msg: "settings need a signed-in user"}
	}
	row, found, err := s.Store.UserSettings(ctx, user)
	if err != nil || !found {
		return 0, "", time.Time{}, err
	}
	if row.TeamCT != nil {
		if plain, err := s.Box.Open(row.TeamCT, teamContext(user)); err == nil {
			team, _ = strconv.Atoi(plain)
		}
	}
	if row.TokenCT != nil {
		if plain, err := s.Box.Open(row.TokenCT, tokenContext(user)); err == nil {
			token, expires = plain, row.TokenExpires
		}
	}
	return team, token, expires, nil
}

// Get returns a user's settings, without the token.
func (s *Settings) Get(ctx context.Context, user string) (UserSettingsView, error) {
	team, token, expires, err := s.load(ctx, user)
	if err != nil {
		return UserSettingsView{}, err
	}
	v := UserSettingsView{User: user, TeamID: team}
	if token != "" {
		now := s.now()
		v.Token.Saved = true
		if !expires.IsZero() {
			v.Token.ExpiresAt = expires.UTC().Format(time.RFC3339)
			v.Token.Expired = !now.Before(expires)
			if !v.Token.Expired {
				v.Token.ExpiresIn = ago(expires.Sub(now))
			}
		}
	}
	return v, nil
}

// Update changes a user's team and token. A token past its expiry is refused: it would only fail
// later, at FPL.
func (s *Settings) Update(ctx context.Context, user string, req UserSettingsRequest) (UserSettingsView, error) {
	if s == nil || s.Box == nil {
		return UserSettingsView{}, ErrSettingsUnavailable
	}
	if user == "" {
		return UserSettingsView{}, &InputError{Msg: "settings need a signed-in user"}
	}
	if req.ClearTeam {
		if err := s.Store.SaveUserTeam(ctx, user, nil); err != nil {
			return UserSettingsView{}, err
		}
	}
	if req.TeamID != nil {
		if err := CheckTeamID(*req.TeamID); err != nil {
			return UserSettingsView{}, err
		}
		ct, err := s.Box.Seal(strconv.Itoa(*req.TeamID), teamContext(user))
		if err != nil {
			return UserSettingsView{}, err
		}
		if err := s.Store.SaveUserTeam(ctx, user, ct); err != nil {
			return UserSettingsView{}, err
		}
	}
	switch {
	case req.ClearToken:
		if err := s.Store.SaveUserToken(ctx, user, nil, time.Time{}); err != nil {
			return UserSettingsView{}, err
		}
	case req.Token != nil:
		token, err := NormalizeToken(*req.Token)
		if err != nil {
			return UserSettingsView{}, err
		}
		expires, ok := fpl.TokenExpiry(token)
		if ok && !s.now().Before(expires) {
			return UserSettingsView{}, &InputError{Msg: "that token has already expired: copy a fresh one from the FPL site"}
		}
		ct, err := s.Box.Seal(token, tokenContext(user))
		if err != nil {
			return UserSettingsView{}, fmt.Errorf("encrypt token: %w", err)
		}
		if err := s.Store.SaveUserToken(ctx, user, ct, expires); err != nil {
			return UserSettingsView{}, err
		}
	}
	return s.Get(ctx, user)
}

// Viewer is whose squad a signed-in user's requests are about: their saved team, and their saved
// token while it lasts. With nothing saved it is the zero Viewer (the server's default team).
func (s *Settings) Viewer(ctx context.Context, user string) (Viewer, error) {
	team, token, expires, err := s.load(ctx, user)
	if err != nil {
		return Viewer{}, err
	}
	v := Viewer{TeamID: team}
	if team != 0 && token != "" && (expires.IsZero() || s.now().Before(expires)) {
		v.Token = token
	}
	return v, nil
}
