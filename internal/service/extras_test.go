package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func withDeadline(t time.Time) func(*world) {
	return func(w *world) {
		for i := range w.events {
			if w.events[i].ID == 8 {
				w.events[i].Name = "Gameweek 8"
				w.events[i].DeadlineTime = t.Format(time.RFC3339)
			}
		}
	}
}

func TestStatus(t *testing.T) {
	r := newRig(t, rigOpts{mutate: withDeadline(testNow.Add(50*time.Hour + 20*time.Minute))})

	got, err := r.svc.Status(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.Gameweek != 8 || got.GameweekName != "Gameweek 8" || got.LastFinished != 7 || got.HistoryThrough != 7 {
		t.Errorf("season = %+v", got)
	}
	if got.DeadlineIn != "2d 2h" {
		t.Errorf("deadline in = %q, want 2d 2h", got.DeadlineIn)
	}
	if got.Players != 45 || len(got.Clubs) != 12 || got.Clubs[0] != (service.ClubView{Short: "C01", Name: "Club1"}) {
		t.Errorf("players %d clubs %v", got.Players, got.Clubs)
	}
	if got.NewsArticles != 12 || !got.NewsSearch || !got.TeamConfigured {
		t.Errorf("news %d search %v team %v", got.NewsArticles, got.NewsSearch, got.TeamConfigured)
	}
	if got.Session.State != "not configured" || got.Session.Configured {
		t.Errorf("session = %+v", got.Session)
	}
	if got.Notes == nil {
		t.Error("notes must be an empty list, not null")
	}
}

func TestStatus_DeadlinePassedAndNoEmbedder(t *testing.T) {
	r := newRig(t, rigOpts{noEmbed: true, mutate: withDeadline(testNow.Add(-time.Minute))})

	got, err := r.svc.Status(context.Background())

	if err != nil || got.DeadlineIn != "passed" || got.NewsSearch {
		t.Errorf("err %v, %+v", err, got)
	}
}

func TestStatus_Session(t *testing.T) {
	tests := []struct {
		name, session, state, expires string
	}{
		{"valid", token(testNow.Add(47 * time.Minute)), "valid", "47 min"},
		{"expired", token(testNow.Add(-time.Hour)), "expired", ""},
		{"not a token", "Bearer opaque", "unknown expiry", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, rigOpts{live: &fakeLive{}, session: tt.session})

			got, err := r.svc.Status(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			if !got.Session.Configured || got.Session.State != tt.state || got.Session.ExpiresIn != tt.expires {
				t.Errorf("session = %+v", got.Session)
			}
		})
	}
}

func TestStatus_NeverContactsLiveFPLOrLeaksTheToken(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(time.Hour))})

	got, _ := r.svc.Status(context.Background())

	if live.calls != 0 {
		t.Errorf("the dashboard called live FPL %d times", live.calls)
	}
	if b, _ := jsonOf(got); contains(b, "SECRET") {
		t.Errorf("token material in the status: %s", b)
	}
}

func TestBuildSquad_IsLegalWithinBudgetAndPicksTheStrongPlayers(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{})

	if err != nil {
		t.Fatal(err)
	}
	if got.Gameweek != 8 || got.Horizon != 4 || got.Budget != "£100.0m" {
		t.Errorf("header = %+v", got)
	}
	if len(got.Starting) != 11 || len(got.Bench) != 4 {
		t.Fatalf("starting %d bench %d", len(got.Starting), len(got.Bench))
	}
	all := append(append([]service.PlayerView{}, got.Starting...), got.Bench...)
	perClub := map[string]int{}
	for _, p := range all {
		perClub[p.Team]++
		if perClub[p.Team] > 3 {
			t.Errorf("more than three players from %s", p.Team)
		}
	}
	if !contains(names(got.Starting), "Mkt00") { // the 9-a-week goalkeeper is the obvious pick
		t.Errorf("starting XI = %v, want the strong goalkeeper Mkt00", names(got.Starting))
	}
	if got.Captain == "" || got.ViceCaptain == "" || got.Captain == got.ViceCaptain || got.Formation == "" || got.ExpectedPoints <= 0 {
		t.Errorf("captain %q vice %q formation %q xp %v", got.Captain, got.ViceCaptain, got.Formation, got.ExpectedPoints)
	}
	if got.Cost > got.Budget && len(got.Cost) >= len(got.Budget) {
		t.Errorf("cost %s over budget %s", got.Cost, got.Budget)
	}
}

func TestBuildSquad_BadHorizonIsAnInputError(t *testing.T) {
	r := newRig(t, rigOpts{})
	for _, h := range []int{-1, 11} {
		_, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{Horizon: h})
		var in *service.InputError
		if !errors.As(err, &in) {
			t.Errorf("horizon %d: err = %v, want an InputError", h, err)
		}
	}
}

func TestBuildSquad_DoesNotNeedTheManagersTeam(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.entryErr = errors.New("FPL is down")

	if _, err := r.svc.BuildSquad(context.Background(), service.SquadRequest{Horizon: 2}); err != nil {
		t.Errorf("the squad builder must not depend on the manager's squad: %v", err)
	}
}

func TestBacktest(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Backtest(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.FirstGameweek != 3 || got.LastGameweek != 7 || len(got.Models) != 7 || len(got.PerGameweek) != 5 || len(got.WalkForward) != 3 {
		t.Errorf("shape = %d-%d models %d per-gw %d walk %d", got.FirstGameweek, got.LastGameweek, len(got.Models), len(got.PerGameweek), len(got.WalkForward))
	}
	if got.WalkForward[0].TunedOn != "GW3-4" || got.Default.RateDecay == 0 {
		t.Errorf("walk-forward %+v default %+v", got.WalkForward[0], got.Default)
	}
	if !hasNote(got.Notes, "Only 5 gameweeks") {
		t.Errorf("a short history must be flagged: %v", got.Notes)
	}
}

func TestBacktest_IsKeptUntilTheHistoryChanges(t *testing.T) {
	r := newRig(t, rigOpts{})
	first, err := r.svc.Backtest(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	*r.now = r.now.Add(10 * time.Minute) // past the snapshot TTL: the data is re-read, unchanged
	again, err := r.svc.Backtest(context.Background())
	if err != nil || again.LastGameweek != first.LastGameweek || len(again.Models) != len(first.Models) {
		t.Fatalf("same history, different answer: %v %+v", err, again)
	}

	for _, p := range r.store.w.players { // GW8 is played
		r.store.w.history = append(r.store.w.history, fpl.PlayerHistory{PlayerID: p.ID, GameweekID: 8, Fixture: p.ID*10 + 8, TotalPoints: 3, Minutes: 90})
	}
	*r.now = r.now.Add(10 * time.Minute)
	updated, err := r.svc.Backtest(context.Background())
	if err != nil || updated.LastGameweek != 8 {
		t.Errorf("new history must refresh the backtest: %v last=%d", err, updated.LastGameweek)
	}
}

func TestBacktest_TooLittleHistory(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		var keep []fpl.PlayerHistory
		for _, h := range w.history {
			if h.GameweekID <= 2 {
				keep = append(keep, h)
			}
		}
		w.history = keep
	}})

	_, err := r.svc.Backtest(context.Background())

	if err == nil {
		t.Fatal("two gameweeks cannot be backtested")
	}
}

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func contains[T comparable](xs any, want T) bool {
	switch v := xs.(type) {
	case string:
		w, _ := any(want).(string)
		return strings.Contains(v, w)
	case []T:
		return slices.Contains(v, want)
	}
	return false
}

func TestPlayers_PagesAreStableAndCoverEveryMatchExactlyOnce(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := context.Background()

	var seen []string
	total := 0
	for offset := 0; ; offset += 20 {
		page, err := r.svc.Players(ctx, service.PlayerQuery{Limit: 20, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if page.Offset != offset || page.Limit != 20 {
			t.Errorf("page header = %+v", page)
		}
		total = page.Total
		if len(page.Players) == 0 {
			break
		}
		seen = append(seen, names(page.Players)...)
	}

	if total != 45 || len(seen) != 45 {
		t.Fatalf("total %d, players seen %d, want 45 each", total, len(seen))
	}
	dup := map[string]bool{}
	for _, n := range seen {
		if dup[n] {
			t.Errorf("%s appeared on two pages", n)
		}
		dup[n] = true
	}
}

func TestPlayers_TotalIsTheMatchesNotThePage(t *testing.T) {
	r := newRig(t, rigOpts{})

	page, err := r.svc.Players(context.Background(), service.PlayerQuery{Position: "GKP", Limit: 2})

	if err != nil {
		t.Fatal(err)
	}
	if len(page.Players) != 2 || page.Total != 7 { // 2 squad goalkeepers + 5 in the market
		t.Errorf("page %d total %d, want 2 and 7", len(page.Players), page.Total)
	}
}

func TestPlayers_OffsetPastTheEndIsAnEmptyPageWithTheTotal(t *testing.T) {
	r := newRig(t, rigOpts{})

	page, err := r.svc.Players(context.Background(), service.PlayerQuery{Offset: 500})

	if err != nil || len(page.Players) != 0 || page.Total != 45 || page.Players == nil {
		t.Errorf("err %v page %+v (players must be an empty list, not null)", err, page)
	}
}

func TestPlayers_SortColumnsAndDirections(t *testing.T) {
	r := newRig(t, rigOpts{})
	tests := []struct {
		name  string
		q     service.PlayerQuery
		check func(a, b service.PlayerView) bool // a is listed before b
	}{
		{"price defaults to cheapest first", service.PlayerQuery{SortBy: "price"}, func(a, b service.PlayerView) bool { return a.Price <= b.Price || len(a.Price) < len(b.Price) }},
		{"price descending", service.PlayerQuery{SortBy: "price", SortDir: "desc"}, func(a, b service.PlayerView) bool { return a.Price >= b.Price || len(a.Price) > len(b.Price) }},
		{"xp defaults to best first", service.PlayerQuery{}, func(a, b service.PlayerView) bool { return a.XP >= b.XP }},
		{"xp ascending", service.PlayerQuery{SortDir: "asc"}, func(a, b service.PlayerView) bool { return a.XP <= b.XP }},
		{"name is A to Z", service.PlayerQuery{SortBy: "name"}, func(a, b service.PlayerView) bool { return strings.ToLower(a.Name) <= strings.ToLower(b.Name) }},
		{"name descending", service.PlayerQuery{SortBy: "name", SortDir: "desc"}, func(a, b service.PlayerView) bool { return strings.ToLower(a.Name) >= strings.ToLower(b.Name) }},
		{"team", service.PlayerQuery{SortBy: "team"}, func(a, b service.PlayerView) bool { return a.Team <= b.Team }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.q.Limit = 100
			page, err := r.svc.Players(context.Background(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i < len(page.Players); i++ {
				if !tt.check(page.Players[i-1], page.Players[i]) {
					t.Fatalf("%s before %s breaks the order", page.Players[i-1].Name, page.Players[i].Name)
				}
			}
		})
	}
}

func TestPlayers_PageSizeIsCappedAt100ButFindPlayersStaysAt25(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := context.Background()

	page, _ := r.svc.Players(ctx, service.PlayerQuery{Limit: 500, Status: "any"})
	found, _ := r.svc.FindPlayers(ctx, service.PlayerQuery{Limit: 500, Status: "any", Offset: 10})

	if page.Limit != 100 || len(page.Players) != 45 {
		t.Errorf("table page: limit %d players %d", page.Limit, len(page.Players))
	}
	if len(found) != 25 {
		t.Errorf("the assistant's lookup returned %d, want the 25 cap", len(found))
	}
	if found[0].Name != page.Players[0].Name {
		t.Errorf("FindPlayers must ignore the offset: first %s vs %s", found[0].Name, page.Players[0].Name)
	}
}

func TestPlayers_BadPagingAndSortAreInputErrors(t *testing.T) {
	r := newRig(t, rigOpts{})
	for _, q := range []service.PlayerQuery{{Offset: -1}, {SortDir: "sideways"}, {SortBy: "shoe size"}} {
		_, err := r.svc.Players(context.Background(), q)
		var in *service.InputError
		if !errors.As(err, &in) {
			t.Errorf("%+v: err = %v, want an InputError", q, err)
		}
	}
}

// The manager pastes a fresh token into .env while the server is running. The next
// request must use it: neither a restart nor the three-minute cache may stand in the way.
func TestANewTokenTakesEffectImmediatelyAndNotAfterTheCacheExpires(t *testing.T) {
	tk := token(testNow.Add(-time.Hour)) // expired
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, sessionFn: func() string { return tk }})
	ctx := context.Background()

	before, err := r.svc.MyTeam(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Source != "public" || live.calls != 0 || !hasNote(before.Notes, "fresh token") {
		t.Fatalf("expired token: source %q live calls %d notes %v", before.Source, live.calls, before.Notes)
	}

	tk = token(testNow.Add(40 * time.Minute)) // pasted into .env; the clock has not moved
	after, err := r.svc.MyTeam(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if after.Source != "live" || live.calls != 1 {
		t.Errorf("fresh token: source %q live calls %d, want live and 1 call, with no wait for the cache", after.Source, live.calls)
	}
}

func TestAnUnchangedTokenStillUsesTheCache(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	tk := token(testNow.Add(time.Hour))
	r := newRig(t, rigOpts{live: live, sessionFn: func() string { return tk }})

	for range 3 {
		if _, err := r.svc.MyTeam(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if live.calls != 1 {
		t.Errorf("live FPL was called %d times for one token; the cache must still work", live.calls)
	}
}

func TestStatusShowsASessionPastedAfterStartup(t *testing.T) {
	tk := ""
	r := newRig(t, rigOpts{live: &fakeLive{}, sessionFn: func() string { return tk }})
	ctx := context.Background()

	none, _ := r.svc.Status(ctx)
	tk = token(testNow.Add(30 * time.Minute))
	now, _ := r.svc.Status(ctx)

	if none.Session.State != "not configured" || now.Session.State != "valid" || now.Session.ExpiresIn != "30 min" {
		t.Errorf("before %+v, after %+v", none.Session, now.Session)
	}
}

func TestStatus_NamesTheGameweekInPlay(t *testing.T) {
	inPlay := func(w *world) { // GW8's deadline has passed and its matches are being played; GW9 is next
		for i := range w.events {
			switch w.events[i].ID {
			case 8:
				w.events[i].IsNext, w.events[i].IsCurrent = false, true
			case 9:
				w.events[i].IsNext = true
			}
		}
	}
	live := newRig(t, rigOpts{mutate: inPlay})
	between := newRig(t, rigOpts{})

	a, err := live.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := between.svc.Status(context.Background())

	if a.LiveGameweek != 8 || a.Gameweek != 9 {
		t.Errorf("GW8 is in play and GW9 is next: live %d next %d", a.LiveGameweek, a.Gameweek)
	}
	if b.LiveGameweek != 0 {
		t.Errorf("between gameweeks nothing is live, got %d", b.LiveGameweek)
	}
}
