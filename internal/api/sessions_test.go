package api

import (
	"context"
	"testing"
	"time"
)

func TestSessions(t *testing.T) {
	clock := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	create := func() Conversation { return nil }

	t.Run("an id gets the same conversation back, and busy blocks a second use", func(t *testing.T) {
		s := newSessions(5, time.Hour, now)
		id, _, release, err := s.acquire("", create)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := s.acquire(id, create); err != errSessionBusy {
			t.Errorf("err = %v, want busy", err)
		}
		release()
		if _, _, rel2, err := s.acquire(id, create); err != nil {
			t.Errorf("after release: %v", err)
		} else {
			rel2()
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		s := newSessions(5, time.Hour, now)
		if _, _, _, err := s.acquire("nope", create); err != errUnknownSession {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a full table evicts the least recently used idle conversation", func(t *testing.T) {
		s := newSessions(2, time.Hour, now)
		old, _, rel, _ := s.acquire("", create)
		rel()
		clock = clock.Add(time.Minute)
		newer, _, rel, _ := s.acquire("", create)
		rel()
		clock = clock.Add(time.Minute)

		if _, _, rel, err := s.acquire("", create); err != nil {
			t.Fatalf("third conversation: %v", err)
		} else {
			rel()
		}

		if _, _, _, err := s.acquire(old, create); err != errUnknownSession {
			t.Errorf("the oldest should have been evicted, err = %v", err)
		}
		if _, _, rel, err := s.acquire(newer, create); err != nil {
			t.Errorf("the newer one should survive: %v", err)
		} else {
			rel()
		}
	})

	t.Run("a table full of busy conversations refuses a new one", func(t *testing.T) {
		s := newSessions(1, time.Hour, now)
		if _, _, _, err := s.acquire("", create); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := s.acquire("", create); err != errTooManySession {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("sweep forgets idle conversations but never a busy one", func(t *testing.T) {
		s := newSessions(5, time.Hour, now)
		idle, _, rel, _ := s.acquire("", create)
		rel()
		busy, _, _, _ := s.acquire("", create)
		clock = clock.Add(2 * time.Hour)

		s.sweep()

		if _, _, _, err := s.acquire(idle, create); err != errUnknownSession {
			t.Errorf("idle conversation survived: %v", err)
		}
		if len(s.m) != 1 || s.m[busy] == nil {
			t.Errorf("the busy conversation must stay: %v", s.m)
		}
	})

	t.Run("the janitor stops when its context is cancelled", func(t *testing.T) {
		s := newSessions(5, time.Hour, now)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.janitor(ctx, time.Millisecond); close(done) }()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("janitor goroutine did not stop")
		}
	})
}
