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

	client := fpl.NewHTTPClient(&http.Client{Timeout: 15 * time.Second})

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
