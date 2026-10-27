package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
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
)

// viewerFrom reads the team and token headers. The token is validated without being
// echoed anywhere: an error says the header is malformed, never what it held.
func viewerFrom(r *http.Request) (service.Viewer, error) {
	var v service.Viewer
	if t := strings.TrimSpace(r.Header.Get(teamHeader)); t != "" {
		id, err := strconv.Atoi(t)
		if err != nil {
			return v, &service.InputError{Msg: "the team id must be a number such as 1234567"}
		}
		if err := service.CheckTeamID(id); err != nil {
			return v, err
		}
		v.TeamID = id
	}
	if raw := strings.TrimSpace(r.Header.Get(tokenHeader)); raw != "" {
		token, err := service.NormalizeToken(raw)
		if err != nil {
			return v, err
		}
		v.Token = token
	}
	return v, nil
}

// localUser is the one user of a server without a login proxy: the manager on their own machine.
const localUser = "local"

type userKey struct{}

// userFrom names who is asking. Behind the login proxy that is the signed-in account, from the
// headers the proxy sets after checking the login (its email, else its username); the proxy
// replaces any such header a browser sends. Without a proxy it is the local user. Empty means
// signed in but unnamed, which gets no saved settings.
func (s *Server) userFrom(r *http.Request) string {
	if len(s.cfg.UserHeaders) == 0 {
		return localUser
	}
	for _, h := range s.cfg.UserHeaders {
		if v := strings.ToLower(strings.TrimSpace(r.Header.Get(h))); v != "" {
			return v
		}
	}
	return ""
}

func userOf(ctx context.Context) string {
	u, _ := ctx.Value(userKey{}).(string)
	return u
}

// owner names a viewer for binding a conversation to them. The token is hashed: the
// session table never holds one.
func owner(v service.Viewer) string {
	sum := sha256.Sum256([]byte(v.Token))
	return strconv.Itoa(v.TeamID) + ":" + hex.EncodeToString(sum[:8])
}

// withViewer puts the user and the viewer in the request's context. A request that names no
// team gets the user's saved team and token; one that names the saved team without a token gets
// the saved token. A saved token is never applied to any other team.
func (s *Server) withViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := viewerFrom(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		user := s.userFrom(r)
		if s.cfg.Settings != nil && user != "" && v.Token == "" {
			saved, err := s.cfg.Settings.Viewer(r.Context(), user)
			switch {
			case errors.Is(err, service.ErrSettingsUnavailable):
			case err != nil:
				s.fail(w, r, err)
				return
			case v.TeamID == 0:
				v = saved
			case v.TeamID == saved.TeamID:
				v.Token = saved.Token
			}
		}
		ctx := context.WithValue(service.WithViewer(r.Context(), v), userKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
