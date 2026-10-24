package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	errUnknownSession = errors.New("unknown or expired conversation; start a new one")
	errSessionBusy    = errors.New("this conversation is still answering the previous question")
	errTooManySession = errors.New("too many conversations are in progress; try again shortly")
)

// sessions holds conversations in memory. A conversation is dropped after sitting idle,
// and when the table is full the least recently used idle one makes room.
type sessions struct {
	mu   sync.Mutex
	m    map[string]*entry
	max  int
	idle time.Duration
	now  func() time.Time
}

type entry struct {
	conv Conversation
	last time.Time
	busy bool // one question at a time per conversation
}

func newSessions(max int, idle time.Duration, now func() time.Time) *sessions {
	return &sessions{m: map[string]*entry{}, max: max, idle: idle, now: now}
}

// acquire marks a conversation busy and returns it; the caller must call release.
// An empty id starts a new conversation.
func (s *sessions) acquire(id string, create func() Conversation) (string, Conversation, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var e *entry
	if id == "" {
		if len(s.m) >= s.max && !s.evictOldestIdle() {
			return "", nil, nil, errTooManySession
		}
		var err error
		if id, err = newID(); err != nil {
			return "", nil, nil, err
		}
		e = &entry{conv: create()}
		s.m[id] = e
	} else if e = s.m[id]; e == nil {
		return "", nil, nil, errUnknownSession
	}
	if e.busy {
		return "", nil, nil, errSessionBusy
	}
	e.busy, e.last = true, s.now()
	return id, e.conv, func() {
		s.mu.Lock()
		e.busy, e.last = false, s.now()
		s.mu.Unlock()
	}, nil
}

// evictOldestIdle frees one slot, reporting whether it could. Callers hold s.mu.
func (s *sessions) evictOldestIdle() bool {
	oldest, at := "", time.Time{}
	for id, e := range s.m {
		if !e.busy && (oldest == "" || e.last.Before(at)) {
			oldest, at = id, e.last
		}
	}
	if oldest == "" {
		return false
	}
	delete(s.m, oldest)
	return true
}

// sweep forgets conversations idle for longer than the limit.
func (s *sessions) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.m {
		if !e.busy && s.now().Sub(e.last) > s.idle {
			delete(s.m, id)
		}
	}
}

// janitor sweeps every interval until ctx is cancelled.
func (s *sessions) janitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(max(every, time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep()
		}
	}
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
