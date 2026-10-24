// Package notify tells the manager about something: in the app's inbox, and on their phone
// through ntfy.sh when a topic is set.
package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
)

// Message is one notification. PlayerID is 0 when it is about no one player.
type Message struct {
	Kind     string // "watch", "briefing", "health"
	Title    string
	Body     string
	URL      string // where tapping it goes; empty for none
	PlayerID int
}

// Notifier delivers a message.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// AlertStore is the part of db.Store the inbox needs.
type AlertStore interface {
	AddAlert(ctx context.Context, a db.Alert) (int64, error)
}

// Inbox stores messages as alerts, shown on the app's Alerts page.
type Inbox struct{ Store AlertStore }

func (i Inbox) Notify(ctx context.Context, m Message) error {
	_, err := i.Store.AddAlert(ctx, db.Alert{Kind: m.Kind, Title: m.Title, Body: m.Body, URL: m.URL, PlayerID: m.PlayerID})
	return err
}

// Ntfy pushes messages to a phone through an ntfy server. Anyone who knows the topic can read
// it, so the topic is treated like a password and lives in .env.
type Ntfy struct {
	Server string // e.g. https://ntfy.sh
	Topic  string
	Client *http.Client
}

// maxNtfyBody keeps a push short: the full text is in the inbox.
const maxNtfyBody = 1000

func (n Ntfy) Notify(ctx context.Context, m Message) error {
	body := m.Body
	if len(body) > maxNtfyBody {
		body = body[:maxNtfyBody] + "…"
	}
	if body == "" {
		body = m.Title
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.Server, "/")+"/"+n.Topic, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("ntfy request: %w", err)
	}
	// Headers carry only ASCII safely; ntfy decodes RFC 2047 for anything else ("João").
	req.Header.Set("Title", headerText(m.Title))
	req.Header.Set("Tags", tag(m.Kind))
	if m.URL != "" && (strings.HasPrefix(m.URL, "https://") || strings.HasPrefix(m.URL, "http://")) {
		req.Header.Set("Click", m.URL)
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy push: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy push: status %d", resp.StatusCode)
	}
	return nil
}

func headerText(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

// tag picks the emoji ntfy shows next to a message.
func tag(kind string) string {
	switch kind {
	case "watch":
		return "eyes"
	case "briefing":
		return "soccer"
	case "health":
		return "warning"
	}
	return "bell"
}

// Multi sends to the inbox first and then to every other notifier. The inbox is the record: if
// it fails nothing else is tried, so a retry cannot push a message the app never stored. A failed
// push is returned but the stored alert stays.
type Multi struct {
	Inbox  Notifier
	Pushes []Notifier
}

func (m Multi) Notify(ctx context.Context, msg Message) error {
	if err := m.Inbox.Notify(ctx, msg); err != nil {
		return fmt.Errorf("store alert: %w", err)
	}
	var errs []error
	for _, p := range m.Pushes {
		if err := p.Notify(ctx, msg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
