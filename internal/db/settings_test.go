package db_test

import (
	"context"
	"testing"
	"time"
)

func TestStore_UserSettings(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	if _, found, err := s.UserSettings(ctx, "alice"); err != nil || found {
		t.Fatalf("a new user has no row: found=%v err=%v", found, err)
	}

	exp := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	if err := s.SaveUserToken(ctx, "alice", []byte("token-ct"), exp); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUserTeam(ctx, "alice", []byte("team-ct")); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.UserSettings(ctx, "alice")
	if err != nil || !found || string(got.TeamCT) != "team-ct" || string(got.TokenCT) != "token-ct" || !got.TokenExpires.Equal(exp) {
		t.Fatalf("saving the team must keep the token: %+v found=%v err=%v", got, found, err)
	}

	if err := s.SaveUserToken(ctx, "alice", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.UserSettings(ctx, "alice")
	if got.TokenCT != nil || !got.TokenExpires.IsZero() || string(got.TeamCT) != "team-ct" {
		t.Errorf("clearing the token must keep the team: %+v", got)
	}

	if _, found, _ := s.UserSettings(ctx, "bob"); found {
		t.Error("rows are per user")
	}
}
