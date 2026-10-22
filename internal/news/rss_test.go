package news_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
)

// bbcFeed mirrors the real BBC Sport feed: GMT timestamps, a tracking query
// string on the link, and a guid that is not the link.
const bbcFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:media="http://search.yahoo.com/mrss/" version="2.0">
<channel><title>BBC Sport</title>
<item>
  <title><![CDATA[Saka injury 'does not look good' - how will Arsenal cope?]]></title>
  <description><![CDATA[Arsenal's Bukayo Saka limps off &amp; faces a scan.]]></description>
  <link>https://www.bbc.co.uk/sport/football/articles/ckg5j1zdn4m3o?at_medium=RSS&amp;at_campaign=rss</link>
  <guid isPermaLink="false">https://www.bbc.co.uk/sport/football/articles/ckg5j1zdn4m3o#0</guid>
  <pubDate>Wed, 21 Oct 2026 22:51:56 GMT</pubDate>
</item>
</channel></rss>`

// skyFeed mirrors the real Sky Sports feed: BST timestamps, an XSL processing
// instruction, an HTML description, and a live blog with no description at all.
const skyFeed = `<?xml version="1.0" encoding="utf-8" ?>
<?xml-stylesheet title="XSL_formatting" type="text/xsl" href="/rss/rssRender.xsl"?>
<rss version="2.0"><channel><title>SkySports | News</title>
<item>
  <title>Papers: Brighton boss Hurzeler would be Shearer's pick</title>
  <description><![CDATA[<p>Newcastle legend <b>Alan Shearer</b> says&nbsp;Hurzeler fits.</p>]]></description>
  <link>https://www.skysports.com/football/news/12040/13602091/manchester-united</link>
  <pubDate>Wed, 21 Oct 2026 23:23:00 BST</pubDate>
</item>
<item>
  <title>Brighton latest: Hurzeler will not let Seagulls underestimate Jablonec</title>
  <description></description>
  <link>https://www.skysports.com/football/live-blog/12040/13279295/brighton</link>
  <pubDate>Wed, 21 Oct 2026 22:47:00 BST</pubDate>
</item>
</channel></rss>`

func TestParseRSS_BBC(t *testing.T) {
	items, skipped, err := news.ParseRSS(strings.NewReader(bbcFeed), "bbc")

	if err != nil || skipped != 0 || len(items) != 1 {
		t.Fatalf("items=%d skipped=%d err=%v; want 1, 0, nil", len(items), skipped, err)
	}
	it := items[0]
	if it.Source != "bbc" {
		t.Errorf("source = %q", it.Source)
	}
	if it.Title != "Saka injury 'does not look good' - how will Arsenal cope?" {
		t.Errorf("title = %q", it.Title)
	}
	// Entities are decoded exactly once.
	if it.Summary != "Arsenal's Bukayo Saka limps off & faces a scan." {
		t.Errorf("summary = %q", it.Summary)
	}
	// The tracking query string would otherwise make the same article look new
	// every time and defeat de-duplication.
	if want := "https://www.bbc.co.uk/sport/football/articles/ckg5j1zdn4m3o"; it.URL != want {
		t.Errorf("url = %q, want %q", it.URL, want)
	}
	if want := time.Date(2026, 10, 21, 22, 51, 56, 0, time.UTC); !it.PublishedAt.Equal(want) {
		t.Errorf("published = %v, want %v", it.PublishedAt, want)
	}
}

func TestParseRSS_SkyBSTIsConvertedToUTC(t *testing.T) {
	items, _, err := news.ParseRSS(strings.NewReader(skyFeed), "sky")
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%d err=%v; want 2", len(items), err)
	}

	// 23:23 BST is 22:23 UTC. Go reads "BST" as UTC+0 unless told otherwise, which
	// would make every Sky story an hour newer than it is and break recency ranking.
	if want := time.Date(2026, 10, 21, 22, 23, 0, 0, time.UTC); !items[0].PublishedAt.Equal(want) {
		t.Errorf("published = %v, want %v", items[0].PublishedAt.UTC(), want)
	}
	if items[0].PublishedAt.Location() != time.UTC {
		t.Errorf("location = %v, want UTC so stored times are unambiguous", items[0].PublishedAt.Location())
	}
}

func TestParseRSS_CleansHTMLAndKeepsEmptyDescriptions(t *testing.T) {
	items, _, _ := news.ParseRSS(strings.NewReader(skyFeed), "sky")

	if want := "Newcastle legend Alan Shearer says Hurzeler fits."; items[0].Summary != want {
		t.Errorf("summary = %q, want tags stripped and entities decoded: %q", items[0].Summary, want)
	}
	// A live blog has a title but no blurb; it is still worth keeping.
	if items[1].Summary != "" || items[1].Title == "" {
		t.Errorf("live blog = %+v, want a title and an empty summary", items[1])
	}
}

func TestParseRSS_SkipsBrokenItemsButKeepsTheRest(t *testing.T) {
	feed := `<rss version="2.0"><channel>
	<item><title>No link</title><pubDate>Wed, 21 Oct 2026 22:00:00 GMT</pubDate></item>
	<item><link>https://x.test/a</link><pubDate>Wed, 21 Oct 2026 22:00:00 GMT</pubDate></item>
	<item><title>Bad date</title><link>https://x.test/b</link><pubDate>last Tuesday</pubDate></item>
	<item><title>Good</title><link>https://x.test/c</link><pubDate>Wed, 21 Oct 2026 22:00:00 GMT</pubDate></item>
	</channel></rss>`

	items, skipped, err := news.ParseRSS(strings.NewReader(feed), "x")

	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Good" || skipped != 3 {
		t.Errorf("items=%v skipped=%d; want only 'Good' and 3 skipped", items, skipped)
	}
}

func TestParseRSS_BadInput(t *testing.T) {
	if _, _, err := news.ParseRSS(strings.NewReader("<html><body>not a feed"), "x"); err == nil {
		t.Error("want an error for non-XML input")
	}
	items, skipped, err := news.ParseRSS(strings.NewReader(`<rss version="2.0"><channel></channel></rss>`), "x")
	if err != nil || len(items) != 0 || skipped != 0 {
		t.Errorf("empty channel: items=%d skipped=%d err=%v; want a quiet empty result", len(items), skipped, err)
	}
}

func TestItem_Text(t *testing.T) {
	tests := []struct {
		name string
		item news.Item
		want string
	}{
		{"title and summary", news.Item{Title: "Saka doubt", Summary: "Hamstring."}, "Saka doubt. Hamstring."},
		{"title only", news.Item{Title: "Brighton latest"}, "Brighton latest"},
		{"title already punctuated", news.Item{Title: "Is Saka fit?", Summary: "Scan today."}, "Is Saka fit? Scan today."},
	}
	for _, tc := range tests {
		if got := tc.item.Text(); got != tc.want {
			t.Errorf("%s: Text() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRSSSource_FetchesParsesAndIdentifiesItself(t *testing.T) {
	var gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotPath = r.Header.Get("User-Agent"), r.URL.Path
		_, _ = w.Write([]byte(bbcFeed))
	}))
	t.Cleanup(srv.Close)
	src := news.NewRSSSource("bbc", srv.URL+"/feed.xml", srv.Client())

	items, err := src.Items(context.Background())

	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if src.Name() != "bbc" || gotPath != "/feed.xml" {
		t.Errorf("name=%q path=%q", src.Name(), gotPath)
	}
	// Publishers can see who is polling them; be an identifiable, polite client.
	if !strings.Contains(gotUA, "lets-go-gaffer") {
		t.Errorf("User-Agent = %q, want it to name this project", gotUA)
	}
}

func TestRSSSource_HTTPErrorIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := news.NewRSSSource("sky", srv.URL, srv.Client()).Items(context.Background())

	var se *httpx.StatusError
	if !errors.As(err, &se) || se.StatusCode != 403 {
		t.Fatalf("err = %v, want *httpx.StatusError{403}", err)
	}
	if !strings.Contains(err.Error(), "sky") {
		t.Errorf("err = %q, want it to say which feed failed", err)
	}
}
