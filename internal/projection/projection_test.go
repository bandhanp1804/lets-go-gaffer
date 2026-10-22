package projection_test

import (
	"math"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/projection"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func player(id, team int, pos fpl.Position) fpl.Player {
	return fpl.Player{ID: id, TeamID: team, Position: pos, Status: "a"}
}

// rows builds history for one player: one 90-minute appearance per entry, the
// entry being that gameweek's points (gameweeks start at 1).
func rows(id int, pts ...int) []fpl.PlayerHistory {
	var out []fpl.PlayerHistory
	for i, p := range pts {
		out = append(out, fpl.PlayerHistory{PlayerID: id, GameweekID: i + 1, Fixture: id*100 + i, TotalPoints: p, Minutes: 90})
	}
	return out
}

// plain has no shrinkage, no recency weighting, and no fixture adjustment, so a
// prediction is simply the player's season average.
var plain = projection.Params{Decay: 1}

func model(t *testing.T, players []fpl.Player, h []fpl.PlayerHistory, fx fpl.Fixtures, p projection.Params, next int) *projection.Model {
	t.Helper()
	m, err := projection.New(players, h, fx, p, next)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExpected_WithNoAdjustmentsIsTheSeasonAverage(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID)}

	m := model(t, players, rows(1, 2, 8, 5, 5), nil, plain, 5)

	if got := m.Expected(players[0], 5); !near(got, 5) { // (2+8+5+5)/4
		t.Errorf("expected = %v, want the season average 5", got)
	}
}

func TestExpected_DecayFavoursRecentGameweeks(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 1, fpl.MID)}
	history := append(rows(1, 10, 10, 0, 0), rows(2, 0, 0, 10, 10)...) // same total, opposite timing
	flat := model(t, players, history, nil, plain, 5)
	decayed := model(t, players, history, nil, projection.Params{Decay: 0.5}, 5)

	if a, b := flat.Expected(players[0], 5), flat.Expected(players[1], 5); !near(a, b) {
		t.Fatalf("with no decay both average %v and %v, want equal", a, b)
	}
	if early, recent := decayed.Expected(players[0], 5), decayed.Expected(players[1], 5); recent <= early {
		t.Errorf("recent scorer %v should beat early scorer %v when recent weeks count more", recent, early)
	}
}

func TestExpected_MinutesCanBeRememberedForLessTimeThanPerformance(t *testing.T) {
	// Both players score 6 per 90 minutes all season. A has played every minute; B played
	// every minute until the latest two gameweeks, when he was dropped to 30.
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 2, fpl.MID)}
	a := rows(1, 6, 6, 6, 6, 6, 6)
	b := rows(2, 6, 6, 6, 6, 2, 2)
	b[4].Minutes, b[5].Minutes = 30, 30
	history := append(a, b...)

	long := model(t, players, history, nil, projection.Params{Decay: 1}, 7)
	short := model(t, players, history, nil, projection.Params{Decay: 1, MinutesDecay: 0.2}, 7)

	// Same performance memory in both, so his rate is 6 per 90 either way...
	if got := long.Expected(players[0], 7); !near(got, 6) {
		t.Fatalf("regular = %v, want 6", got)
	}
	// ...but a short minutes memory sees he is now playing a third of the time.
	if l, s := long.Expected(players[1], 7), short.Expected(players[1], 7); !(s < l) {
		t.Errorf("with minutes remembered briefly he should be expected to score less: long %v, short %v", l, s)
	}
	if s := short.Expected(players[1], 7); s > 6*0.5 {
		t.Errorf("short-memory estimate %v should be close to a third of 6, well under half", s)
	}
}

func TestExpected_ZeroMinutesDecayMeansTheSameAsDecay(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID)}
	h := rows(1, 2, 9, 4, 7)
	a := model(t, players, h, nil, projection.Params{Decay: 0.6}, 5).Expected(players[0], 5)
	b := model(t, players, h, nil, projection.Params{Decay: 0.6, MinutesDecay: 0.6}, 5).Expected(players[0], 5)

	if a != b {
		t.Errorf("unset MinutesDecay gave %v, explicit equal value gave %v", a, b)
	}
}

func TestExpected_ShrinksSmallSamplesTowardThePositionAverage(t *testing.T) {
	// A is one lucky game; B and C are long, ordinary seasons that set the norm (about 4 per game).
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 2, fpl.MID), player(3, 3, fpl.MID)}
	history := append(append(rows(1, 15), rows(2, 4, 4, 4, 4, 4, 4)...), rows(3, 4, 4, 4, 4, 4, 4)...)
	raw := model(t, players, history, nil, projection.Params{Decay: 1}, 7).Expected(players[0], 7)
	mild := model(t, players, history, nil, projection.Params{Decay: 1, PriorGames: 2}, 7).Expected(players[0], 7)
	strong := model(t, players, history, nil, projection.Params{Decay: 1, PriorGames: 8}, 7).Expected(players[0], 7)

	// His one game happened in a season of 6 gameweeks, so unshrunk he averages 15/6.
	if !near(raw, 15.0/6) {
		t.Fatalf("unshrunk = %v, want 2.5", raw)
	}
	// The position norm is 63 points over 13 full games; at his share of a sixth that is the floor.
	floor := (63.0 / 13) / 6
	// Shrinking lowers an inflated estimate, more shrinking lowers it further, and
	// since he really is above the norm he never drops to it.
	if !(floor < strong && strong < mild && mild < raw) {
		t.Errorf("want floor %v < strong %v < mild %v < unshrunk %v", floor, strong, mild, raw)
	}
}

func TestExpected_MinutesScaleTheExpectation(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 2, fpl.MID)}
	full := rows(1, 6, 6, 6, 6)
	third := rows(2, 2, 2, 2, 2) // 30 minutes a week at the same 6 points per 90
	for i := range third {
		third[i].Minutes = 30
	}

	m := model(t, players, append(full, third...), nil, plain, 5)

	if a, b := m.Expected(players[0], 5), m.Expected(players[1], 5); !near(a, 6) || !near(b, 2) {
		t.Errorf("full-time %v and part-time %v, want 6 and 2: same rate, a third of the minutes", a, b)
	}
}

func TestExpected_FixtureDifficultyMovesTheExpectation(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID)} // team 1
	fx := fpl.Fixtures{
		{ID: 1, Event: 5, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 2, AwayDifficulty: 4}, // easy
		{ID: 2, Event: 6, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 3, AwayDifficulty: 3}, // neutral
		{ID: 3, Event: 7, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 4, AwayDifficulty: 2}, // hard
		// event 8 is a blank for team 1
		{ID: 4, Event: 9, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 3, AwayDifficulty: 3}, // double gameweek...
		{ID: 5, Event: 9, HomeTeam: 3, AwayTeam: 1, HomeDifficulty: 3, AwayDifficulty: 2}, // ...second match is easy for team 1
	}
	m := model(t, players, rows(1, 5, 5, 5, 5), fx, projection.Params{Decay: 1, FixtureSlope: 0.1}, 5)
	p := players[0]

	easy, neutral, hard := m.Expected(p, 5), m.Expected(p, 6), m.Expected(p, 7)

	if !near(neutral, 5) || !near(easy, 5.5) || !near(hard, 4.5) {
		t.Errorf("easy %v, neutral %v, hard %v; want 5.5, 5, 4.5 at a slope of 10%% per point", easy, neutral, hard)
	}
	if blank := m.Expected(p, 8); blank != 0 {
		t.Errorf("a blank gameweek is worth %v, want 0", blank)
	}
	// Two matches: each is worth the neutral 5 adjusted by its own difficulty (3 -> x1.0, and 3 -> x1.0, with AwayDifficulty 2 -> x1.1).
	if double := m.Expected(p, 9); !near(double, 5*1.0+5*1.1) {
		t.Errorf("a double gameweek is worth %v, want %v", double, 5*1.0+5*1.1)
	}
}

func TestExpected_MissingFixtureDataMeansNeutralNotBlank(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID)}

	m := model(t, players, rows(1, 5, 5, 5, 5), nil, projection.Params{Decay: 1, FixtureSlope: 0.1}, 5)

	if got := m.Expected(players[0], 5); !near(got, 5) {
		t.Errorf("with no fixture list at all got %v, want 5: absence of data is not a blank gameweek", got)
	}
}

func TestExpected_NeverUsesTheFuture(t *testing.T) {
	// A backtest is only honest if predicting gameweek 5 cannot see gameweek 5.
	players := []fpl.Player{player(1, 1, fpl.MID)}
	past := rows(1, 4, 4, 4, 4)
	withFuture := append(append([]fpl.PlayerHistory(nil), past...),
		fpl.PlayerHistory{PlayerID: 1, GameweekID: 5, Fixture: 999, TotalPoints: 40, Minutes: 90},
		fpl.PlayerHistory{PlayerID: 1, GameweekID: 6, Fixture: 998, TotalPoints: 40, Minutes: 90})
	params := projection.Params{Decay: 0.7, PriorGames: 2}

	a := model(t, players, past, nil, params, 5).Expected(players[0], 5)
	b := model(t, players, withFuture, nil, params, 5).Expected(players[0], 5)

	if a != b {
		t.Errorf("prediction changed from %v to %v when later gameweeks were added: the model is leaking", a, b)
	}
}

func TestExpected_PlayerWithNoHistoryScoresZero(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 1, fpl.FWD)}

	m := model(t, players, rows(1, 5, 5), nil, projection.Params{Decay: 0.8, PriorGames: 3}, 3)

	if got := m.Expected(players[1], 3); got != 0 {
		t.Errorf("a player who has never played is expected to score %v, want 0", got)
	}
	if got := m.Expected(player(99, 1, fpl.MID), 3); got != 0 {
		t.Errorf("an unknown player scores %v, want 0", got)
	}
}

func TestScore_AveragesTheHorizonAndAppliesAvailability(t *testing.T) {
	fit := player(1, 1, fpl.MID)
	doubt := player(2, 1, fpl.MID)
	pct := 50
	doubt.Status, doubt.ChanceOfPlayingNextRound = "d", &pct
	hurt := player(3, 1, fpl.MID)
	hurt.Status = "i"
	players := []fpl.Player{fit, doubt, hurt}
	history := append(append(rows(1, 6, 6, 6, 6), rows(2, 6, 6, 6, 6)...), rows(3, 6, 6, 6, 6)...)
	fx := fpl.Fixtures{
		{ID: 1, Event: 5, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 2, AwayDifficulty: 4}, // x1.1 at slope 0.1
		{ID: 2, Event: 6, HomeTeam: 1, AwayTeam: 2, HomeDifficulty: 4, AwayDifficulty: 2}, // x0.9
	}
	m := model(t, players, history, fx, projection.Params{Decay: 1, FixtureSlope: 0.1}, 5)

	score := m.Score(2)

	if got := score(fit); !near(got, (6*1.1+6*0.9)/2) {
		t.Errorf("fit player = %v, want the mean of 6.6 and 5.4 = 6", got)
	}
	if got := score(doubt); !near(got, 3) {
		t.Errorf("a 50%% doubt = %v, want half of 6", got)
	}
	if got := score(hurt); got != 0 {
		t.Errorf("an injured player = %v, want 0", got)
	}
}

func TestNew_RejectsBadParameters(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID)}
	for name, p := range map[string]projection.Params{
		"decay of zero":           {Decay: 0},
		"decay above one":         {Decay: 1.5},
		"negative prior":          {Decay: 1, PriorGames: -1},
		"slope too steep":         {Decay: 1, FixtureSlope: 0.6},
		"negative slope":          {Decay: 1, FixtureSlope: -0.1},
		"minutes decay above one": {Decay: 1, MinutesDecay: 1.2},
		"negative minutes decay":  {Decay: 1, MinutesDecay: -0.2},
	} {
		if _, err := projection.New(players, nil, nil, p, 5); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := projection.New(players, nil, nil, plain, 1); err == nil {
		t.Error("predicting gameweek 1 has no history to learn from: want an error")
	}
}

func TestSpearman(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical order", []float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}, 1},
		{"reversed order", []float64{1, 2, 3, 4}, []float64{40, 30, 20, 10}, -1},
		{"one swap", []float64{1, 2, 3, 4}, []float64{1, 3, 2, 4}, 0.8},
		{"ties share an average rank", []float64{1, 1, 2, 3}, []float64{1, 1, 2, 3}, 1},
		{"a constant side has no ordering", []float64{1, 1, 1}, []float64{1, 2, 3}, 0},
	}
	for _, tc := range tests {
		if got := projection.Spearman(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEvaluate_AnOraclePredictorScoresPerfectlyAndTheReverseWorst(t *testing.T) {
	var players []fpl.Player
	var history []fpl.PlayerHistory
	for id := 1; id <= 40; id++ {
		players = append(players, player(id, 1+id%5, fpl.MID))
		// Everyone has played before (so they are in the pool) and scores id points in gameweek 4.
		history = append(history, fpl.PlayerHistory{PlayerID: id, GameweekID: 3, Fixture: id, TotalPoints: 1, Minutes: 90})
		history = append(history, fpl.PlayerHistory{PlayerID: id, GameweekID: 4, Fixture: 1000 + id, TotalPoints: id, Minutes: 90})
	}
	oracle := func(next int) func(fpl.Player) float64 {
		return func(p fpl.Player) float64 { return float64(p.ID) }
	}
	reverse := func(next int) func(fpl.Player) float64 {
		return func(p fpl.Player) float64 { return -float64(p.ID) }
	}

	best := projection.Evaluate(players, history, 4, 4, oracle)
	worst := projection.Evaluate(players, history, 4, 4, reverse)

	if !near(best.Spearman, 1) || !near(best.MAE, 0) || best.Weeks != 1 {
		t.Errorf("oracle = %+v, want Spearman 1, MAE 0, 1 week", best)
	}
	if !near(worst.Spearman, -1) {
		t.Errorf("reverse = %+v, want Spearman -1", worst)
	}
	if best.TopMean <= worst.TopMean {
		t.Errorf("the oracle's top picks (%v) should out-score the reverse's (%v)", best.TopMean, worst.TopMean)
	}
}

func TestEvaluate_SkipsWeeksWithTooFewPlayersToJudge(t *testing.T) {
	players := []fpl.Player{player(1, 1, fpl.MID), player(2, 1, fpl.MID)}
	history := append(rows(1, 3, 4, 5), rows(2, 1, 2, 3)...)

	got := projection.Evaluate(players, history, 3, 3, func(int) func(fpl.Player) float64 {
		return func(p fpl.Player) float64 { return float64(p.ID) }
	})

	if got.Weeks != 0 {
		t.Errorf("weeks = %d, want 0: two players are not enough to say anything", got.Weeks)
	}
}
