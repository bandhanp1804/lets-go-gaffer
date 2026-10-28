package notify_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
)

func TestNtfy_PostsTheMessageToTheTopic(t *testing.T) {
	tests := []struct {
		name      string
		msg       notify.Message
		wantTitle string
		wantClick string
		wantBody  string
		wantTags  string
	}{
		{"plain", notify.Message{Kind: "watch", Title: "Saka: doubtful", Body: "75% chance", URL: "https://bbc.test/s"},
			"Saka: doubtful", "https://bbc.test/s", "75% chance", "eyes"},
		{"accented title is encoded", notify.Message{Kind: "briefing", Title: "João Pedro"},
			"=?UTF-8?B?Sm/Do28gUGVkcm8=?=", "", "João Pedro", "soccer"},
		{"a non-web link is not clickable", notify.Message{Kind: "health", Title: "x", Body: "y", URL: "fpl://player/1"},
			"x", "", "y", "warning"},
		{"long body is cut", notify.Message{Title: "t", Body: strings.Repeat("a", 1200)},
			"t", "", strings.Repeat("a", 1000) + "…", "bell"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *http.Request
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got, body = r, string(b)
			}))
			defer srv.Close()

			err := notify.Ntfy{Server: srv.URL + "/", Topic: "gaffer-secret", Client: srv.Client()}.Notify(context.Background(), tt.msg)

			if err != nil {
				t.Fatal(err)
			}
			if got.Method != "POST" || got.URL.Path != "/gaffer-secret" {
				t.Errorf("%s %s", got.Method, got.URL.Path)
			}
			if got.Header.Get("Title") != tt.wantTitle || got.Header.Get("Click") != tt.wantClick || got.Header.Get("Tags") != tt.wantTags || body != tt.wantBody {
				t.Errorf("title=%q click=%q tags=%q body=%q", got.Header.Get("Title"), got.Header.Get("Click"), got.Header.Get("Tags"), body)
			}
		})
	}
}

func TestNtfy_AFailedPushIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer srv.Close()

	err := notify.Ntfy{Server: srv.URL, Topic: "t", Client: srv.Client()}.Notify(context.Background(), notify.Message{Title: "x"})

	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err = %v", err)
	}
}

type alerts struct {
	got []db.Alert
	err error
}

func (a *alerts) AddAlert(_ context.Context, al db.Alert) (int64, error) {
	if a.err != nil {
		return 0, a.err
	}
	a.got = append(a.got, al)
	return int64(len(a.got)), nil
}

type push struct {
	n   int
	err error
}

func (p *push) Notify(context.Context, notify.Message) error { p.n++; return p.err }

func TestMulti(t *testing.T) {
	msg := notify.Message{Kind: "watch", Title: "Palmer: suspended", PlayerID: 4}

	t.Run("stores, then pushes", func(t *testing.T) {
		store, p := &alerts{}, &push{}
		err := notify.Multi{Inbox: notify.Inbox{Store: store}, Pushes: []notify.Notifier{p}}.Notify(context.Background(), msg)
		if err != nil || len(store.got) != 1 || store.got[0].PlayerID != 4 || p.n != 1 {
			t.Errorf("err=%v stored=%+v pushes=%d", err, store.got, p.n)
		}
	})
	t.Run("a failed push keeps the stored alert", func(t *testing.T) {
		store, p := &alerts{}, &push{err: errors.New("offline")}
		err := notify.Multi{Inbox: notify.Inbox{Store: store}, Pushes: []notify.Notifier{p}}.Notify(context.Background(), msg)
		if err == nil || len(store.got) != 1 {
			t.Errorf("err=%v stored=%d", err, len(store.got))
		}
	})
	t.Run("a visitor's alert is stored for them and never pushed", func(t *testing.T) {
		store, p := &alerts{}, &push{}
		visitor := msg
		visitor.User = "visitor@x.com"
		err := notify.Multi{Inbox: notify.Inbox{Store: store}, Pushes: []notify.Notifier{p}}.Notify(context.Background(), visitor)
		if err != nil || len(store.got) != 1 || store.got[0].User != "visitor@x.com" || p.n != 0 {
			t.Errorf("err=%v stored=%+v pushes=%d", err, store.got, p.n)
		}
	})
	t.Run("nothing is pushed if storing fails", func(t *testing.T) {
		store, p := &alerts{err: errors.New("db down")}, &push{}
		err := notify.Multi{Inbox: notify.Inbox{Store: store}, Pushes: []notify.Notifier{p}}.Notify(context.Background(), msg)
		if err == nil || p.n != 0 {
			t.Errorf("err=%v pushes=%d", err, p.n)
		}
	})
}
