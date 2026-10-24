package service

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// TestLive_PointsAgreeWithFPLsOwnFigure checks the scoring rule against the real API: for every
// finished gameweek of one team, the points computed from the live data and the picks must equal
// the points FPL itself reports for that gameweek. It reads only the one team you name.
//
// Run it with: GAFFER_LIVE_FPL_TEAM=<your team id> go test ./internal/service -run TestLive_Points -v
func TestLive_PointsAgreeWithFPLsOwnFigure(t *testing.T) {
	id, _ := strconv.Atoi(os.Getenv("GAFFER_LIVE_FPL_TEAM"))
	if id == 0 {
		t.Skip("set GAFFER_LIVE_FPL_TEAM to a team id to run this against FPL")
	}
	ctx := context.Background()
	hc := &http.Client{Transport: httpx.NewRateLimitTransport(http.DefaultTransport, httpx.NewLimiter(300*time.Millisecond)), Timeout: 30 * time.Second}
	c := fpl.NewHTTPClient(hc)
	boot, err := c.FetchBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := &baseSnap{players: boot.Players, short: map[int]string{}}
	checked := 0
	for gw := 1; gw <= fpl.LastFinishedGameweek(boot.Gameweeks); gw++ {
		picks, err := c.FetchPicks(ctx, id, gw)
		if err != nil {
			t.Logf("GW%d: no picks (%v)", gw, err)
			continue
		}
		live, err := c.FetchLive(ctx, gw)
		if err != nil {
			t.Fatal(err)
		}
		v := scorePicks(gw, false, picks, live, base)
		chip := "-"
		if picks.ActiveChip != nil {
			chip = *picks.ActiveChip
		}
		t.Logf("GW%d chip %-9s computed net %3d (gross %3d, hit %d, bench %2d) FPL says %3d  autosubs %d", gw, chip, v.Net, v.Gross, v.Hit, v.OnBench, picks.EntryHistory.Points, len(picks.AutomaticSubs))
		if v.Net != picks.EntryHistory.Points {
			t.Errorf("GW%d: computed %d, FPL reports %d", gw, v.Net, picks.EntryHistory.Points)
		}
		checked++
	}
	if checked == 0 {
		t.Error("no gameweek could be checked")
	}
}
