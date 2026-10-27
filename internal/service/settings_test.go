package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/secretbox"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// settingsStore keeps rows in memory, as the database would: only ciphertext.
type settingsStore struct{ rows map[string]db.UserSettings }

func (s *settingsStore) UserSettings(_ context.Context, user string) (db.UserSettings, bool, error) {
	r, ok := s.rows[user]
	return r, ok, nil
}
func (s *settingsStore) SaveUserTeam(_ context.Context, user string, ct []byte) error {
	r := s.rows[user]
	r.TeamCT = ct
	s.rows[user] = r
	return nil
}
func (s *settingsStore) SaveUserToken(_ context.Context, user string, ct []byte, exp time.Time) error {
	r := s.rows[user]
	r.TokenCT, r.TokenExpires = ct, exp
	if ct == nil {
		r.TokenExpires = time.Time{}
	}
	s.rows[user] = r
	return nil
}

func settingsRig(t *testing.T, keyFill byte) (*service.Settings, *settingsStore) {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{keyFill}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	st := &settingsStore{rows: map[string]db.UserSettings{}}
	return &service.Settings{Store: st, Box: box, Now: func() time.Time { return testNow }}, st
}

func ptr[T any](v T) *T { return &v }

func TestSettings_SaveTeamAndTokenEncryptedAndNeverSendTheTokenBack(t *testing.T) {
	s, st := settingsRig(t, 1)
	ctx := context.Background()
	tok := token(testNow.Add(50 * time.Minute))

	v, err := s.Update(ctx, "alice@example.com", service.UserSettingsRequest{TeamID: ptr(1234567), Token: ptr(tok)})
	if err != nil {
		t.Fatal(err)
	}
	if v.TeamID != 1234567 || !v.Token.Saved || v.Token.Expired || v.Token.ExpiresIn != "50 min" {
		t.Errorf("view = %+v", v)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "SECRET") {
		t.Errorf("the token leaked into the response: %s", b)
	}
	row := st.rows["alice@example.com"]
	if bytes.Contains(row.TeamCT, []byte("1234567")) || bytes.Contains(row.TokenCT, []byte("SECRET")) {
		t.Error("the database row holds plain text")
	}

	viewer, err := s.Viewer(ctx, "alice@example.com")
	if err != nil || viewer.TeamID != 1234567 || viewer.Token != tok {
		t.Errorf("viewer = %+v, %v", viewer, err)
	}
}

func TestSettings_AnExpiredTokenIsRefusedAndAnExpiringOneStopsBeingUsed(t *testing.T) {
	s, _ := settingsRig(t, 1)
	ctx := context.Background()

	_, err := s.Update(ctx, "alice", service.UserSettingsRequest{Token: ptr(token(testNow.Add(-time.Minute)))})
	var ie *service.InputError
	if !errors.As(err, &ie) || !strings.Contains(ie.Msg, "expired") {
		t.Fatalf("err = %v, want an expired-token message", err)
	}

	if _, err := s.Update(ctx, "alice", service.UserSettingsRequest{TeamID: ptr(42), Token: ptr(token(testNow.Add(time.Minute)))}); err != nil {
		t.Fatal(err)
	}
	later := &service.Settings{Store: s.Store, Box: s.Box, Now: func() time.Time { return testNow.Add(2 * time.Minute) }}
	v, _ := later.Get(ctx, "alice")
	viewer, _ := later.Viewer(ctx, "alice")
	if !v.Token.Expired || viewer.Token != "" || viewer.TeamID != 42 {
		t.Errorf("after expiry: view %+v viewer %+v; the team stays, the token is not used", v.Token, viewer)
	}
}

func TestSettings_ClearTokenKeepsTheTeam(t *testing.T) {
	s, _ := settingsRig(t, 1)
	ctx := context.Background()
	_, _ = s.Update(ctx, "alice", service.UserSettingsRequest{TeamID: ptr(42), Token: ptr(token(testNow.Add(time.Hour)))})

	v, err := s.Update(ctx, "alice", service.UserSettingsRequest{ClearToken: true})

	if err != nil || v.Token.Saved || v.TeamID != 42 {
		t.Errorf("%+v %v", v, err)
	}
	v, err = s.Update(ctx, "alice", service.UserSettingsRequest{ClearTeam: true})
	if err != nil || v.TeamID != 0 {
		t.Errorf("clear team: %+v %v", v, err)
	}
}

func TestSettings_RowsCannotBeReadAcrossUsersOrWithAnotherKey(t *testing.T) {
	s, st := settingsRig(t, 1)
	ctx := context.Background()
	_, _ = s.Update(ctx, "alice", service.UserSettingsRequest{TeamID: ptr(42), Token: ptr(token(testNow.Add(time.Hour)))})

	// Mallory's row given Alice's ciphertext: it does not open as Mallory's.
	st.rows["mallory"] = st.rows["alice"]
	if v, err := s.Viewer(ctx, "mallory"); err != nil || v != (service.Viewer{}) {
		t.Errorf("mallory got %+v (%v): a copied row must not open", v, err)
	}

	// A new key reads old rows as unset, so the user is asked again rather than locked out.
	rekeyed, _ := settingsRig(t, 2)
	rekeyed.Store = st
	if v, err := rekeyed.Get(ctx, "alice"); err != nil || v.TeamID != 0 || v.Token.Saved {
		t.Errorf("with another key: %+v %v", v, err)
	}
}

func TestSettings_InputAndAvailability(t *testing.T) {
	s, _ := settingsRig(t, 1)
	ctx := context.Background()
	var ie *service.InputError

	for _, req := range []service.UserSettingsRequest{
		{TeamID: ptr(0)},
		{TeamID: ptr(1_000_000_000)},
		{Token: ptr("not a token!")},
		{Token: ptr("")},
	} {
		if _, err := s.Update(ctx, "alice", req); !errors.As(err, &ie) {
			t.Errorf("%+v: err = %v, want an input error", req, err)
		}
	}
	if _, err := s.Get(ctx, ""); !errors.As(err, &ie) {
		t.Errorf("no user: %v", err)
	}
	off := &service.Settings{Store: &settingsStore{}}
	if _, err := off.Get(ctx, "alice"); !errors.Is(err, service.ErrSettingsUnavailable) {
		t.Errorf("no key: %v", err)
	}
	var none *service.Settings
	if _, err := none.Viewer(ctx, "alice"); !errors.Is(err, service.ErrSettingsUnavailable) {
		t.Errorf("nil settings: %v", err)
	}
}

func TestNormalizeToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc.def.ghi", "Bearer abc.def.ghi"},
		{"Bearer abc.def.ghi", "Bearer abc.def.ghi"},
		{"  bearer   abc.def.ghi  ", "Bearer abc.def.ghi"},
	}
	for _, tt := range tests {
		if got, err := service.NormalizeToken(tt.in); err != nil || got != tt.want {
			t.Errorf("%q = %q, %v", tt.in, got, err)
		}
	}
}
