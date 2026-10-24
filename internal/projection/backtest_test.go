package projection_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

// season builds 40 players over n gameweeks where a player's points are steady
// (player i scores i/4 + 1 a week), so a sensible model should rank them well.
func season(n int) ([]fpl.Player, []fpl.PlayerHistory) {
	var players []fpl.Player
	var h []fpl.PlayerHistory
	for i := 1; i <= 40; i++ {
		players = append(players, player(i, i%20+1, fpl.Position(i%4+1)))
		pts := make([]int, n)
		for g := range pts {
			pts[g] = i/4 + 1
		}
		h = append(h, rows(i, pts...)...)
	}
	return players, h
}

func TestRunBacktest_ReportsEveryGameweekAndWalkForwardStep(t *testing.T) {
	players, h := season(7)

	rep, err := projection.RunBacktest(context.Background(), players, h, nil)

	if err != nil {
		t.Fatal(err)
	}
	if rep.First != projection.BacktestFirst || rep.Last != 7 {
		t.Errorf("range = GW%d-%d", rep.First, rep.Last)
	}
	if len(rep.Models) != 7 || len(rep.PerGameweek) != 5 {
		t.Errorf("models %d, per-gameweek %d, want 7 and 5", len(rep.Models), len(rep.PerGameweek))
	}
	// Walk-forward starts two gameweeks after the first judged one: GW5, 6, 7.
	if len(rep.WalkForward) != 3 || rep.WalkForward[0].Gameweek != 5 || rep.WalkForward[0].TunedTo != 4 {
		t.Errorf("walk-forward = %+v", rep.WalkForward)
	}
	if len(rep.Best) != 8 {
		t.Errorf("best settings = %d, want 8", len(rep.Best))
	}
	for i := 1; i < len(rep.Best); i++ {
		if rep.Best[i].Spearman > rep.Best[i-1].Spearman {
			t.Errorf("best settings must be sorted best first: %v then %v", rep.Best[i-1].Spearman, rep.Best[i].Spearman)
		}
	}
	if rep.ModelWins < 0 || rep.ModelWins > len(rep.PerGameweek) {
		t.Errorf("model wins = %d of %d", rep.ModelWins, len(rep.PerGameweek))
	}
}

func TestRunBacktest_TooLittleHistoryIsATypedError(t *testing.T) {
	players, h := season(2)

	_, err := projection.RunBacktest(context.Background(), players, h, nil)

	if !errors.Is(err, projection.ErrTooLittleHistory) {
		t.Errorf("err = %v", err)
	}
}

func TestRunBacktest_StopsWhenCancelled(t *testing.T) {
	players, h := season(7)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := projection.RunBacktest(ctx, players, h, nil)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
