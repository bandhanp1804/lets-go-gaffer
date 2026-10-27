// Package secretbox encrypts small values (a team id, a login token) before they are stored,
// with AES-256-GCM from the standard library. The key lives outside the database (an env var
// from a Kubernetes secret), so a copy of the database or a backup reveals nothing.
//
// Each value is sealed with a context, such as "token:alice", that is authenticated but not
// stored: a ciphertext copied to another user's row, or to another field, fails to open.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// version prefixes every ciphertext, so the format or the key can change later without
// guessing which one a stored value used.
const version byte = 1

// KeySize is the key length in bytes (AES-256).
const KeySize = 32

// ErrOpen means a ciphertext could not be opened: the wrong key, the wrong context, or tampering.
var ErrOpen = errors.New("secretbox: cannot decrypt")

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// FromBase64 returns a Box for a base64 key, as made by `openssl rand -base64 32`.
func FromBase64(s string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("secretbox: the key is not base64 (make one with: openssl rand -base64 32): %w", err)
	}
	return New(key)
}

// Seal encrypts plain, bound to context. Every call uses a fresh random nonce, so sealing the
// same value twice gives different ciphertexts.
func (b *Box) Seal(plain, context string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, []byte(plain), []byte(context)), nil
}

// Open decrypts a value sealed with the same key and context.
func (b *Box) Open(sealed []byte, context string) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return "", ErrOpen
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(context))
	if err != nil {
		return "", ErrOpen
	}
	return string(plain), nil
}
