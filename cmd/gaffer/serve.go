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
	"strings"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/api"
	"github.com/bandhanpatel/lets-go-gaffer/internal/assistant"
	"github.com/bandhanpatel/lets-go-gaffer/internal/llm"
)

const serveUsage = "usage: gaffer serve [-addr 127.0.0.1:8080] [-origins http://localhost:3001,...]"

type serveOpts struct {
	addr    string
	origins []string
}

func parseServeArgs(args []string) (serveOpts, error) {
	var o serveOpts
	var origins string
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8080", "address to listen on (loopback only)")
	fs.StringVar(&origins, "origins", "http://localhost:3001,http://127.0.0.1:3001", "web origins allowed to call the API")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w\n%s", err, serveUsage)
	}
	if fs.NArg() != 0 {
		return o, errors.New(serveUsage)
	}
	if err := requireLoopback(o.addr); err != nil {
		return o, err
	}
	for _, s := range strings.Split(origins, ",") {
		if s = strings.TrimSpace(s); s != "" {
			o.origins = append(o.origins, s)
		}
	}
	return o, nil
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
	svc, closeSvc, err := buildService(ctx)
	if err != nil {
		return err
	}
	defer closeSvc()

	cfg := api.Config{Service: svc, AllowedOrigins: o.origins, Logger: slog.Default()}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		model, err := newAnthropic(key)
		if err != nil {
			return err
		}
		a := assistant.New(model, svc, assistant.Config{Model: llm.ModelFromEnv()})
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
