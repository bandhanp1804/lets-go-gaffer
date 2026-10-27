package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/secretbox"
)

func box(t *testing.T, fill byte) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(bytes.Repeat([]byte{fill}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := box(t, 7)
	for _, plain := range []string{"1234567", "Bearer eyJhbGciOi.payload.sig", "", "ünïcödé"} {
		ct, err := b.Seal(plain, "token:alice")
		if err != nil {
			t.Fatal(err)
		}
		if plain != "" && bytes.Contains(ct, []byte(plain)) {
			t.Errorf("%q appears in its ciphertext", plain)
		}
		got, err := b.Open(ct, "token:alice")
		if err != nil || got != plain {
			t.Errorf("open = %q, %v; want %q", got, err, plain)
		}
	}
}

func TestSealingTwiceDiffers(t *testing.T) {
	b := box(t, 7)
	a, _ := b.Seal("1234567", "team:alice")
	c, _ := b.Seal("1234567", "team:alice")
	if bytes.Equal(a, c) {
		t.Error("the same value sealed twice gave the same bytes: the nonce is not fresh")
	}
}

func TestOpenRefuses(t *testing.T) {
	b := box(t, 7)
	ct, _ := b.Seal("Bearer secret", "token:alice")
	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 1

	tests := []struct {
		name    string
		box     *secretbox.Box
		sealed  []byte
		context string
	}{
		{"another user's row", b, ct, "token:mallory"},
		{"another field", b, ct, "team:alice"},
		{"another key", box(t, 9), ct, "token:alice"},
		{"tampered", b, tampered, "token:alice"},
		{"truncated", b, ct[:10], "token:alice"},
		{"empty", b, nil, "token:alice"},
		{"unknown version", b, append([]byte{9}, ct[1:]...), "token:alice"},
	}
	for _, tt := range tests {
		if _, err := tt.box.Open(tt.sealed, tt.context); !errors.Is(err, secretbox.ErrOpen) {
			t.Errorf("%s: err = %v, want ErrOpen", tt.name, err)
		}
	}
}

func TestKeys(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	tests := []struct {
		key     string
		wantErr string
	}{
		{good, ""},
		{" " + good + "\n", ""},
		{base64.StdEncoding.EncodeToString([]byte("short")), "5 bytes"},
		{"not base64!!", "openssl rand"},
	}
	for _, tt := range tests {
		_, err := secretbox.FromBase64(tt.key)
		if tt.wantErr == "" && err != nil {
			t.Errorf("%q: %v", tt.key, err)
		}
		if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%q: err = %v, want it to mention %q", tt.key, err, tt.wantErr)
		}
	}
}
