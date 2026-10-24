package fpl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Host is FPL's API host, the only place a session token is ever sent.
const Host = "fantasy.premierleague.com"

// ErrSessionRejected means FPL refused the logged-in session: it has expired (they
// last about an hour) or is wrong. It is distinct from FPL being down.
var ErrSessionRejected = errors.New("FPL rejected the session")

type sessionTransport struct {
	base  http.RoundTripper
	value func() string
	host  string
}

// NewSessionTransport sends value, exactly as given, as the X-Api-Authorization
// header on requests to host, which logs the request in as the manager. Nothing
// else is touched:
//
//   - it is only attached for host, so a redirect elsewhere never receives it
//     (Go forwards custom headers across hosts, so it must be added here, on a
//     copy of the request, and not set on the caller's request);
//   - it is refused over plain HTTP except to a loopback address, for tests;
//   - an empty value adds nothing.
//
// The value is never logged or placed in an error by this package.
func NewSessionTransport(base http.RoundTripper, value, host string) http.RoundTripper {
	return NewSessionTransportFunc(base, func() string { return value }, host)
}

// NewSessionTransportFunc is NewSessionTransport for a token that changes while the
// program runs: value is asked for on every request, so a fresh token is used as soon
// as it is available and a long-lived server needs no restart.
func NewSessionTransportFunc(base http.RoundTripper, value func() string, host string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &sessionTransport{base: base, value: value, host: host}
}

type tokenKey struct{}

// WithSessionToken returns a context whose requests carry this token instead of the
// transport's own. A server answering several managers uses it so that each one's
// token is used for that one request only, and is never kept anywhere.
func WithSessionToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

func (t *sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	value := t.value()
	if v, ok := req.Context().Value(tokenKey{}).(string); ok {
		value = v // a request's own token replaces the configured one, even when empty
	}
	if value == "" || req.URL.Host != t.host || !secureOrLoopback(req.URL) {
		return t.base.RoundTrip(req)
	}
	r := req.Clone(req.Context()) // a RoundTripper must not modify the caller's request
	r.Header.Set("X-Api-Authorization", value)
	return t.base.RoundTrip(r)
}

// secureOrLoopback allows https anywhere and plain http only to this machine.
func secureOrLoopback(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TokenExpiry reads when a bearer token expires from its payload. It does not
// verify the token and reads nothing but the expiry. ok is false if the value is
// not a token with an expiry, in which case the caller should simply try it.
func TokenExpiry(value string) (time.Time, bool) {
	token := strings.TrimSpace(value)
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}
