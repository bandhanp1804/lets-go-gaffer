package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/myteam"
)

// Viewer is whose squad a request is about. It travels in the request's context so that
// the service methods, and the assistant's tools that call them, keep their signatures.
//
// The token is a login for the manager's FPL account. It is used for this request, is
// sent to FPL and nowhere else, and is never stored: the cache below keeps only a hash.
type Viewer struct {
	TeamID int    // 0 means the team the server is configured with
	Token  string // empty means no live squad
	// Made is the transfers the manager says they have made since the last deadline. FPL keeps
	// those private until the next deadline, so without a token this is the only way to see
	// them; they are applied to the public squad, checked against FPL's rules, never to a live one.
	Made []myteam.SwapSpec
}

type viewerKey struct{}

// WithViewer attaches a viewer to a context.
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

// ViewerFrom is the viewer attached to a context, or the zero Viewer.
func ViewerFrom(ctx context.Context) Viewer {
	v, _ := ctx.Value(viewerKey{}).(Viewer)
	return v
}

// ErrTeamRequired means no team was given and none is configured.
var ErrTeamRequired = errors.New("enter your FPL team id to see a squad")

// who resolves the team and token a request is for.
//
// A request that names a team gets that team's public squad, and a live one only with a
// token of its own. The server's configured token belongs to the configured team: it is
// never applied to anyone else's.
func (s *Service) who(ctx context.Context) (team int, token string, err error) {
	v := ViewerFrom(ctx)
	team = v.TeamID
	if team == 0 {
		team = s.cfg.TeamID
	}
	if team == 0 {
		return 0, "", ErrTeamRequired
	}
	if v.Token != "" {
		return team, v.Token, nil
	}
	if team == s.cfg.TeamID {
		return team, s.session(), nil
	}
	return team, "", nil
}

// squadKey identifies one cached squad. The token is hashed, so the cache never holds one. The
// transfers a manager entered are part of it: the squad with them is a different squad.
type squadKey struct {
	team  int
	token [sha256.Size]byte
	made  string
}

func newSquadKey(team int, token string, made []myteam.SwapSpec) squadKey {
	k := squadKey{team: team, made: madeKey(made)}
	if token != "" {
		k.token = sha256.Sum256([]byte(token))
	}
	return k
}

// madeKey writes entered transfers the way they arrive, "101>201,113>206".
func madeKey(made []myteam.SwapSpec) string {
	parts := make([]string, len(made))
	for i, m := range made {
		parts[i] = m.Out + ">" + m.In
	}
	return strings.Join(parts, ",")
}

// squadEntry is one cached squad. Its own lock means two requests for the same squad
// share one fetch, while a slow fetch for one manager never blocks another.
type squadEntry struct {
	mu   sync.Mutex
	snap *squadSnap
	at   time.Time

	cmu     sync.Mutex // guards the chip planner's result, which takes a few solves to compute
	chips   *ChipsView
	chipsAt time.Time
}

const maxCachedSquads = 64

// entryFor returns the cache entry for a key, making room by dropping the stalest.
func (s *Service) entryFor(k squadKey) *squadEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.squads == nil {
		s.squads = map[squadKey]*squadEntry{}
	}
	if e, ok := s.squads[k]; ok {
		return e
	}
	if len(s.squads) >= maxCachedSquads {
		var oldest squadKey
		var oldestAt time.Time
		first := true
		for key, e := range s.squads {
			e.mu.Lock()
			at := e.at
			e.mu.Unlock()
			if first || at.Before(oldestAt) {
				oldest, oldestAt, first = key, at, false
			}
		}
		delete(s.squads, oldest)
	}
	e := &squadEntry{}
	s.squads[k] = e
	return e
}
