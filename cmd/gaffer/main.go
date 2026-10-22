package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Retry wraps the rate limiter so every attempt, including retries, is paced.
	// No http.Client.Timeout: it would cut off backoff; ctx bounds the whole call.
	transport := fpl.NewRetryTransport(
		fpl.NewRateLimitTransport(http.DefaultTransport, fpl.NewLimiter(100*time.Millisecond)),
		fpl.RetryConfig{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second},
	)
	client := fpl.NewHTTPClient(&http.Client{Transport: transport})

	bootstrap, err := client.FetchBootstrap(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("players: %d  teams: %d  gameweeks: %d\n",
		len(bootstrap.Players),
		len(bootstrap.Teams),
		len(bootstrap.Gameweeks),
	)

	// Print the first player as a quick sanity check.
	if len(bootstrap.Players) > 0 {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(bootstrap.Players[0])
	}
}
