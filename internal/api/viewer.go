package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// Headers the web client sends to say whose squad a request is about. They are headers,
// not query parameters, so the token never appears in a URL, an access log or the browser
// history; and a custom header makes a cross-site request need a CORS preflight, which
// this server never answers.
const (
	teamHeader  = "X-Gaffer-Team"
	tokenHeader = "X-Gaffer-Token"

	maxTokenLen = 4096
	maxTeamID   = 999_999_999
)

// tokenBody is what a bearer token may contain: a JWT is base64url text and dots.
var tokenBody = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// viewerFrom reads the team and token headers. The token is validated without being
// echoed anywhere: an error says the header is malformed, never what it held.
func viewerFrom(r *http.Request) (service.Viewer, error) {
	var v service.Viewer
	if t := strings.TrimSpace(r.Header.Get(teamHeader)); t != "" {
		id, err := strconv.Atoi(t)
		if err != nil || id < 1 || id > maxTeamID {
			return v, &service.InputError{Msg: "the team id must be a number such as 1253022"}
		}
		v.TeamID = id
	}
	if raw := strings.TrimSpace(r.Header.Get(tokenHeader)); raw != "" {
		if len(raw) > maxTokenLen {
			return v, &service.InputError{Msg: "the token is too long to be an FPL token"}
		}
		body := raw
		if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
			body = strings.TrimSpace(raw[7:])
		}
		if !tokenBody.MatchString(body) {
			return v, &service.InputError{Msg: "that does not look like an FPL token: paste only the value after \"Bearer\""}
		}
		v.Token = "Bearer " + body
	}
	return v, nil
}

// owner names a viewer for binding a conversation to them. The token is hashed: the
// session table never holds one.
func owner(v service.Viewer) string {
	sum := sha256.Sum256([]byte(v.Token))
	return strconv.Itoa(v.TeamID) + ":" + hex.EncodeToString(sum[:8])
}

// withViewer puts the viewer in the request's context for the service to read.
func (s *Server) withViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := viewerFrom(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(service.WithViewer(r.Context(), v)))
	})
}
