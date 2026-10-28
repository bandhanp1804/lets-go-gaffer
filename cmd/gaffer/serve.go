package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/api"
	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
	"github.com/bandhanpatel/lets-go-gaffer/internal/news"
	"github.com/bandhanpatel/lets-go-gaffer/internal/secretbox"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
	"github.com/bandhanpatel/lets-go-gaffer/internal/workflows"
)

const serveUsage = "usage: gaffer serve [-addr 127.0.0.1:8080] [-origins http://localhost:3001,...] [-public -hosts gaffer.example.com,...]"

type serveOpts struct {
	addr    string
	origins []string
	// public is for running behind a login proxy (the Kubernetes ingress): the server may listen
	// beyond loopback, and answers requests whose Host is one of hosts.
	public bool
	hosts  []string
}

func parseServeArgs(args []string) (serveOpts, error) {
	var o serveOpts
	var origins string
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8080", "address to listen on (loopback only)")
	fs.StringVar(&origins, "origins", "http://localhost:3001,http://127.0.0.1:3001", "web origins allowed to call the API")
	var hosts string
	fs.BoolVar(&o.public, "public", false, "listen beyond loopback; only behind a proxy that logs people in")
	fs.StringVar(&hosts, "hosts", "", "with -public, the Host names this server answers to")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w\n%s", err, serveUsage)
	}
	if fs.NArg() != 0 {
		return o, errors.New(serveUsage)
	}
	o.origins, o.hosts = splitList(origins), splitList(hosts)
	switch {
	case o.public && len(o.hosts) == 0:
		return o, errors.New("-public needs -hosts: the names the proxy sends in Host, such as gaffer.example.com")
	case !o.public && len(o.hosts) > 0:
		return o, errors.New("-hosts is only for -public")
	case !o.public:
		if err := requireLoopback(o.addr); err != nil {
			return o, err
		}
	}
	if _, _, err := net.SplitHostPort(o.addr); err != nil {
		return o, fmt.Errorf("-addr %q: %w", o.addr, err)
	}
	return o, nil
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// requireLoopback refuses any address that is not this machine. The API has no login and
// hands out the manager's squad and API credits, so it must not be reachable from the network.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("-addr %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("-addr %q: refusing to listen beyond this machine (the API has no login); use 127.0.0.1 or localhost", addr)
	}
	return nil
}

// runServe starts the HTTP API and runs until ctx is cancelled (Ctrl-C), then lets
// requests in flight finish for a few seconds before cutting them off.
func runServe(ctx context.Context, w io.Writer, args []string) error {
	o, err := parseServeArgs(args)
	if err != nil {
		return err
	}
	st, closeSvc, err := buildStack(ctx)
	if err != nil {
		return err
	}
	defer closeSvc()
	svc := st.svc

	cfg := api.Config{Service: svc, AllowedOrigins: o.origins, AllowedHosts: o.hosts, Logger: slog.Default(), Ready: st.store.Ping}
	inbox := &service.Inbox{Store: st.store}
	cfg.Inbox = inbox
	// Saved settings need the encryption key. Without it the server still runs; the Setup page
	// says settings are off and the browser keeps the team itself.
	if key := os.Getenv("GAFFER_DATA_KEY"); key != "" {
		box, err := secretbox.FromBase64(key)
		if err != nil {
			return fmt.Errorf("GAFFER_DATA_KEY: %w", err)
		}
		cfg.Settings = &service.Settings{Store: st.store, Box: box}
	}
	limits, err := dailyLimits(st.store)
	if err != nil {
		return err
	}
	cfg.Limits = limits
	inbox.IsOwner = limits.IsOwner // owners share an inbox; everyone else has their own
	// Daily counts are kept 30 days, as the privacy policy says. The pruning stops, and is waited
	// for, before the database pool closes: this defer runs before closeSvc's.
	pruneCtx, stopPrune := context.WithCancel(ctx)
	pruned := make(chan struct{})
	go func() {
		defer close(pruned)
		service.PruneDaily(pruneCtx, st.store, time.Now, 24*time.Hour)
	}()
	defer func() { stopPrune(); <-pruned }()
	if o.public {
		// The login proxy names the user; the ingress strips these headers from what the
		// browser sends, so only the proxy can set them.
		cfg.UserHeaders = []string{"X-Auth-Request-Email", "X-Auth-Request-User"}
	}
	if addr := temporalAddress(false); addr != "" {
		// With Temporal the worker pulls the news on its schedule; the button starts a workflow.
		c, err := dialTemporal(ctx, addr)
		if err != nil {
			return err
		}
		defer c.Close()
		every, err := parseNewsRefresh(os.Getenv("GAFFER_NEWS_REFRESH"))
		if err != nil {
			return err
		}
		cfg.News = workflows.Refresher{Client: c, Feeds: feedNames(), Every: every}
		inbox.Watches, inbox.TemporalUI = workflows.Watcher{Client: c}, temporalUI()
		fmt.Fprintf(w, "Workflows: Temporal at %s (web UI %s). Run `gaffer worker` to process them.\n", addr, temporalUI())
	} else {
		// The refresher may be using the database pool, so it is stopped (and waited for) before the
		// pool is closed: this defer was registered after closeSvc's, so it runs first.
		newsCtx, stopNews := context.WithCancel(ctx)
		refresher := startNewsRefresh(newsCtx, w, st)
		defer stopNews()
		if refresher != nil {
			cfg.News = localRefresher{refresher}
			defer refresher.Wait()
		}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		model, err := newAnthropic(key)
		if err != nil {
			return err
		}
		a := assistant.New(model, svc, assistant.Config{Model: llm.ModelFromEnv(), Clubs: clubLegend(ctx, svc)})
		cfg.NewConversation = func() api.Conversation { return a.NewSession() }
	} else {
		fmt.Fprintln(w, "ANTHROPIC_API_KEY is not set: /api/ask is off, the other endpoints work.")
	}

	// requestCtx is cancelled first on shutdown, which stops streams that would otherwise
	// keep Shutdown waiting.
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	handler, err := api.New(ctx, cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
	}
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	fmt.Fprintf(w, "gaffer API on http://%s (allowed origins: %s)\n", ln.Addr(), strings.Join(o.origins, ", "))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	cancelRequests()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	<-errc // Serve returns http.ErrServerClosed once Shutdown has run
	return nil
}

// defaultNewsRefresh is how often the server pulls the news by itself. Each pull costs one request
// per feed and, only for stories not seen before, a little embedding.
const defaultNewsRefresh = 30 * time.Minute

// minNewsRefresh stops a typo from hammering the news sites.
const minNewsRefresh = 5 * time.Minute

// parseNewsRefresh reads GAFFER_NEWS_REFRESH: empty is the default, "off" (or "0") turns the timer off
// (the Refresh button still works), anything else is a duration of at least five minutes.
func parseNewsRefresh(v string) (time.Duration, error) {
	switch v = strings.TrimSpace(v); v {
	case "":
		return defaultNewsRefresh, nil
	case "off", "0":
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("GAFFER_NEWS_REFRESH %q: want a duration like 30m, or off: %w", v, err)
	}
	if d < minNewsRefresh {
		return 0, fmt.Errorf("GAFFER_NEWS_REFRESH %q: at least %s, to be polite to the news sites", v, minNewsRefresh)
	}
	return d, nil
}

// startNewsRefresh builds the refresher for the server, and starts its timer unless that is turned
// off. It returns nil when there is no VOYAGE_API_KEY, since stories cannot be embedded without one.
func startNewsRefresh(ctx context.Context, w io.Writer, st stack) *news.Refresher {
	if st.embedder == nil {
		return nil
	}
	r := news.NewRefresher(ctx, newsPipeline{store: st.store, embedder: st.embedder}.refresh)
	every, err := parseNewsRefresh(os.Getenv("GAFFER_NEWS_REFRESH"))
	switch {
	case err != nil:
		fmt.Fprintf(w, "%v: refreshing only on request.\n", err)
	case every > 0:
		r.Every(every)
		fmt.Fprintf(w, "News refreshes every %s (GAFFER_NEWS_REFRESH=off to stop).\n", every)
	}
	return r
}

// Default daily limits per signed-in user, for the features that spend API credits.
var defaultLimits = map[string]struct {
	env string
	n   int
}{
	service.LimitAsk:     {"GAFFER_LIMIT_ASK", 15},
	service.LimitSearch:  {"GAFFER_LIMIT_SEARCH", 40},
	service.LimitRefresh: {"GAFFER_LIMIT_REFRESH", 3},
}

// dailyLimits reads GAFFER_OWNERS (never limited) and the GAFFER_LIMIT_* settings (0 turns one off).
func dailyLimits(store service.UsageStore) (*service.Limits, error) {
	l := &service.Limits{Store: store, Owners: service.ParseOwners(os.Getenv("GAFFER_OWNERS")), Daily: map[string]int{}}
	for kind, d := range defaultLimits {
		l.Daily[kind] = d.n
		if v := strings.TrimSpace(os.Getenv(d.env)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s must be a whole number, 0 or more, got %q", d.env, v)
			}
			l.Daily[kind] = n
		}
	}
	return l, nil
}
