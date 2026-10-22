package news

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

const (
	maxFeedBytes = 5 << 20
	userAgent    = "lets-go-gaffer/0.1 (+https://github.com/Hellblazer1804/lets-go-gaffer)"
)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// ParseRSS reads an RSS 2.0 feed. Items missing a title, link, or parseable date
// are skipped and counted rather than failing the whole feed.
func ParseRSS(r io.Reader, source string) (items []Item, skipped int, err error) {
	var doc struct {
		Items []struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
		} `xml:"channel>item"`
	}
	dec := xml.NewDecoder(io.LimitReader(r, maxFeedBytes))
	// Real feeds contain HTML entities such as &nbsp; that strict XML rejects.
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&doc); err != nil {
		return nil, 0, fmt.Errorf("parse rss: %w", err)
	}

	for _, raw := range doc.Items {
		title := cleanText(raw.Title)
		link := canonicalURL(raw.Link)
		published, ok := parseTime(raw.PubDate)
		if title == "" || link == "" || !ok {
			skipped++
			continue
		}
		items = append(items, Item{
			Source:      source,
			Title:       title,
			Summary:     cleanText(raw.Description),
			URL:         link,
			PublishedAt: published,
		})
	}
	return items, skipped, nil
}

// cleanText removes HTML tags, decodes entities, and collapses whitespace.
func cleanText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ") // Fields also folds non-breaking spaces
}

// canonicalURL drops the query string and fragment. BBC appends tracking
// parameters, which would make one article look new on every fetch.
func canonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// parseTime reads RFC 1123 dates and returns UTC. Go reads the zone abbreviation
// "BST" as UTC+0 unless the local zone happens to know it, which would make every
// Sky story an hour newer than it is; that case is corrected here.
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 MST", "Mon, 2 Jan 2006 15:04:05 -0700"} {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		if name, offset := t.Zone(); name == "BST" && offset == 0 {
			t = t.Add(-time.Hour)
		}
		return t.UTC(), true
	}
	return time.Time{}, false
}

// RSSSource fetches one RSS feed.
type RSSSource struct {
	name string
	url  string
	hc   *http.Client
}

// NewRSSSource returns a source for the feed at url. Give hc a transport with
// retries and rate limiting (package httpx).
func NewRSSSource(name, url string, hc *http.Client) *RSSSource {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &RSSSource{name: name, url: url, hc: hc}
}

func (s *RSSSource) Name() string { return s.name }

func (s *RSSSource) Items(ctx context.Context) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("feed %s: build request: %w", s.name, err)
	}
	// Publishers can see who polls them; identify this project honestly.
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")

	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feed %s: %w", s.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpx.DrainAndClose(resp.Body)
		return nil, fmt.Errorf("feed %s: %w", s.name, &httpx.StatusError{StatusCode: resp.StatusCode})
	}

	items, skipped, err := ParseRSS(resp.Body, s.name)
	if err != nil {
		return nil, fmt.Errorf("feed %s: %w", s.name, err)
	}
	if skipped > 0 {
		slog.Warn("skipped malformed feed items", "feed", s.name, "skipped", skipped)
	}
	return items, nil
}
