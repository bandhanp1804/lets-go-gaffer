package service_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/embed"
	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

type rig struct {
	svc   *service.Service
	store *fakeStore
	fpl   *fakeFPL
	emb   *fakeEmbedder
	now   *time.Time
}

type rigOpts struct {
	live    *fakeLive
	session string
	// sessionFn replaces session with a token that can change while the service runs.
	sessionFn func() string
	noEmbed   bool
	mutate    func(*world)
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	w := newWorld()
	if o.mutate != nil {
		o.mutate(w)
	}
	r := &rig{store: &fakeStore{w: w}, fpl: newFakeFPL(w), emb: &fakeEmbedder{}, now: new(time.Time)}
	*r.now = testNow
	deps := service.Deps{Store: r.store, FPL: r.fpl, Session: o.session, Now: func() time.Time { return *r.now }}
	deps.SessionFn = o.sessionFn
	if o.live != nil {
		deps.Live = o.live
	}
	if !o.noEmbed {
		deps.Embedder = r.emb
	}
	r.svc = service.New(deps, service.Config{TeamID: teamID})
	return r
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func names(ps []service.PlayerView) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func TestMyTeam_PublicSquadIsLabelledAsStale(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.MyTeam(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "public" || got.Gameweek != 8 || got.TeamName != "Test FC" {
		t.Errorf("source %q gameweek %d team %q", got.Source, got.Gameweek, got.TeamName)
	}
	if len(got.Starting) != 11 || len(got.Bench) != 4 || got.Captain != "Own10" || got.ViceCaptain != "Own06" {
		t.Errorf("XI %d bench %d captain %q vice %q", len(got.Starting), len(got.Bench), got.Captain, got.ViceCaptain)
	}
	if got.Bank != "£2.0m" {
		t.Errorf("bank = %q", got.Bank)
	}
	// The model must never be able to present a stale squad as the current one, and it must not
	// have to work out which deadline from other fields: the view states it.
	if got.AsOf != "the GW7 deadline" {
		t.Errorf("squad_as_of = %q, want it stated outright", got.AsOf)
	}
	if !hasNote(got.Notes, "GW7 deadline") {
		t.Errorf("notes %v should say the squad is as of the last deadline", got.Notes)
	}
	first := got.Starting[0]
	if first.Name != "Own01" || first.Fixture != "C02 (H)" || first.Price != "£6.0m" || first.XP <= 0 {
		t.Errorf("first starter = %+v", first)
	}
}

func TestMyTeam_LiveSquadCarriesFPLsOwnNumbers(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(47 * time.Minute))})

	got, err := r.svc.MyTeam(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "live" || got.Bank != "£0.9m" || got.FreeTransfers != 0 || got.TransfersMade != 2 || got.HitSoFar != 4 {
		t.Errorf("%+v", got)
	}
	if got.AsOf != "now (live from FPL)" {
		t.Errorf("squad_as_of = %q", got.AsOf)
	}
	if !slices.Equal(got.ChipsAvailable, []string{"wildcard"}) {
		t.Errorf("chips = %v, want only the available wildcard", got.ChipsAvailable)
	}
	if !hasNote(got.Notes, "live") || hasNote(got.Notes, "GW7 deadline") {
		t.Errorf("notes = %v", got.Notes)
	}
	if got.Starting[0].SellPrice != "£5.9m" {
		t.Errorf("a live squad shows FPL's selling price, got %q", got.Starting[0].SellPrice)
	}
}

func TestMyTeam_FallsBackToPublicDataAndSaysWhy(t *testing.T) {
	tests := []struct {
		name     string
		live     *fakeLive
		session  string
		wantNote string
		wantCall int
	}{
		{"session rejected", &fakeLive{err: fmt.Errorf("x: %w", fpl.ErrSessionRejected)}, token(testNow.Add(time.Hour)), "rejected", 1},
		{"session expired is not even sent", &fakeLive{team: liveTeam(9, 1, 2, 4)}, token(testNow.Add(-2 * time.Hour)), "expired", 0},
		{"FPL unreachable", &fakeLive{err: errors.New("connection refused")}, token(testNow.Add(time.Hour)), "could not be read", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOpts{live: tc.live, session: tc.session})

			got, err := r.svc.MyTeam(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			if got.Source != "public" || tc.live.calls != tc.wantCall {
				t.Errorf("source %q, live calls %d (want %d)", got.Source, tc.live.calls, tc.wantCall)
			}
			if !hasNote(got.Notes, tc.wantNote) || !hasNote(got.Notes, "GW7 deadline") {
				t.Errorf("notes = %v, want one mentioning %q plus the stale warning", got.Notes, tc.wantNote)
			}
			for _, n := range got.Notes {
				if strings.Contains(n, "SECRET") || strings.Contains(n, "Bearer") {
					t.Errorf("a note leaks the credential: %q", n)
				}
			}
		})
	}
}

func TestSnapshotIsSharedAcrossToolCallsAndRefreshedWhenStale(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := context.Background()

	if _, err := r.svc.MyTeam(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Lineup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.FindPlayers(ctx, service.PlayerQuery{Limit: 3}); err != nil {
		t.Fatal(err)
	}

	// One question usually makes several tool calls; they must not each refetch the world.
	if r.fpl.entryCalls != 1 || r.fpl.fixtureCalls != 1 || r.store.listCalls != 1 {
		t.Errorf("entry %d, fixtures %d, player list %d fetches; want 1 each", r.fpl.entryCalls, r.fpl.fixtureCalls, r.store.listCalls)
	}

	*r.now = r.now.Add(10 * time.Minute) // well past the cache lifetime
	if _, err := r.svc.MyTeam(ctx); err != nil {
		t.Fatal(err)
	}
	if r.fpl.entryCalls != 2 {
		t.Errorf("entry fetches = %d, want a refresh once the snapshot is old", r.fpl.entryCalls)
	}
}

func TestLineup(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Lineup(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got.Gameweek != 8 || got.Source != "public" || !strings.Contains(got.Formation, "-") {
		t.Errorf("gameweek %d source %q formation %q", got.Gameweek, got.Source, got.Formation)
	}
	if got.AsOf != "the GW7 deadline" {
		t.Errorf("squad_as_of = %q", got.AsOf)
	}
	if len(got.Starting) != 11 || len(got.Bench) != 4 || got.Bench[0].Position != "GKP" {
		t.Errorf("XI %d bench %d, spare goalkeeper first? %v", len(got.Starting), len(got.Bench), names(got.Bench))
	}
	if got.Captain == "" || got.ViceCaptain == "" || got.ExpectedPoints <= 0 || got.CaptainLead < 0 {
		t.Errorf("captain %q vice %q total %v lead %v", got.Captain, got.ViceCaptain, got.ExpectedPoints, got.CaptainLead)
	}
	if !hasNote(got.Notes, "GW7 deadline") {
		t.Errorf("notes = %v", got.Notes)
	}
	for _, c := range got.CloseCalls {
		if c.Margin < 0 || c.Margin > 0.5 {
			t.Errorf("close call margin %v outside [0, 0.5]", c.Margin)
		}
	}
	if len(got.OtherFormations) == 0 {
		t.Error("want the cost of the other formations")
	}
}

func TestTransfers_FindsTheObviousUpgradeAndCountsFreeTransfers(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Transfers(context.Background(), service.TransfersRequest{})

	if err != nil {
		t.Fatal(err)
	}
	// The history gives five free transfers: one banked per quiet gameweek, none used since GW3.
	if got.FreeTransfers != 5 || got.Bank != "£2.0m" || got.Wildcard {
		t.Errorf("free %d bank %q wildcard %v", got.FreeTransfers, got.Bank, got.Wildcard)
	}
	if got.AsOf != "the GW7 deadline" {
		t.Errorf("squad_as_of = %q", got.AsOf)
	}
	if len(got.Suggestions) == 0 {
		t.Fatal("the squad goalkeeper scores 1 a week and a 9-a-week goalkeeper is on the market for less: want a suggestion")
	}
	top := got.Suggestions[0]
	if top.NetPoints <= 0 || top.Hit != 0 {
		t.Errorf("net %v hit %d", top.NetPoints, top.Hit)
	}
	var sold, bought []string
	for _, m := range top.Moves {
		sold, bought = append(sold, m.Out.Name), append(bought, m.In.Name)
	}
	if !slices.Contains(sold, "Own01") || !slices.Contains(bought, "Mkt00") {
		t.Errorf("top suggestion sells %v buys %v, want Own01 -> Mkt00", sold, bought)
	}
	if top.NetVsFirst != 0 {
		t.Errorf("the first option compares with itself: %v", top.NetVsFirst)
	}
	for i, sg := range got.Suggestions[1:] {
		// Exactly the difference of the two displayed figures, so a reader's subtraction agrees.
		if want := math.Round((sg.NetPoints-top.NetPoints)*10) / 10; sg.NetVsFirst != want {
			t.Errorf("option %d: net_points_vs_first_option = %v, want about %v", i+2, sg.NetVsFirst, want)
		}
	}
	if got.Suggestions[0].Moves[0].Out.SellPrice == "" || top.BankAfter == "" {
		t.Errorf("the sale price and bank after must be shown: %+v", top)
	}
	if !hasNote(got.Notes, "computed") {
		t.Errorf("public data uses computed selling prices, and must say so: %v", got.Notes)
	}
}

// A swap has two prices and two fixtures; the summary pairs each with the right
// player so the model quotes the sentence instead of crossing the fields.
func TestTransfers_MoveSummaryPairsEachPriceAndFixtureWithItsPlayer(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Transfers(context.Background(), service.TransfersRequest{})

	if err != nil || len(got.Suggestions) == 0 {
		t.Fatalf("err %v suggestions %d", err, len(got.Suggestions))
	}
	for _, m := range got.Suggestions[0].Moves {
		want := m.Out.Name + " (" + m.Out.Team + ", " + m.Out.Position + "), sells for " + m.Out.SellPrice + " out, " +
			m.In.Name + " (" + m.In.Team + ", " + m.In.Position + "), costs " + m.In.Price + " in"
		if !strings.HasPrefix(m.Summary, want) {
			t.Errorf("summary = %q, want prefix %q", m.Summary, want)
		}
		if m.Out.Fixture != "" && !strings.Contains(m.Summary, m.Out.Name+" plays "+m.Out.Fixture) {
			t.Errorf("summary %q lacks %s's fixture %q", m.Summary, m.Out.Name, m.Out.Fixture)
		}
	}
}

func TestTransfers_WithNoFreeTransferEveryMoveCostsAHit(t *testing.T) {
	r := newRig(t, rigOpts{})
	zero := 0

	got, err := r.svc.Transfers(context.Background(), service.TransfersRequest{Free: &zero})

	if err != nil {
		t.Fatal(err)
	}
	if got.FreeTransfers != 0 || len(got.Suggestions) == 0 {
		t.Fatalf("free %d suggestions %d", got.FreeTransfers, len(got.Suggestions))
	}
	for _, s := range got.Suggestions {
		if s.Hit != 4*s.Transfers {
			t.Errorf("%d transfers with no free one should cost %d, got %d", s.Transfers, 4*s.Transfers, s.Hit)
		}
	}
}

func TestTransfers_WildcardIsOneUnlimitedRebuildWithNoHit(t *testing.T) {
	r := newRig(t, rigOpts{})

	got, err := r.svc.Transfers(context.Background(), service.TransfersRequest{Wildcard: true})

	if err != nil {
		t.Fatal(err)
	}
	if !got.Wildcard || len(got.Suggestions) != 1 || got.Suggestions[0].Transfers < 1 || got.Suggestions[0].Hit != 0 {
		t.Errorf("%+v", got)
	}
}

func TestTransfers_LiveDataUsesFPLsFreeTransfersAndPrices(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(time.Hour))})

	got, err := r.svc.Transfers(context.Background(), service.TransfersRequest{})

	if err != nil {
		t.Fatal(err)
	}
	if got.FreeTransfers != 0 || got.PendingTransfers != 2 || got.PendingHit != 4 || got.Bank != "£0.9m" {
		t.Errorf("%+v", got)
	}
	if hasNote(got.Notes, "computed") {
		t.Errorf("live prices are FPL's own, not computed: %v", got.Notes)
	}
}

func TestFindPlayers(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		for i := range w.players {
			if w.players[i].ID == 205 {
				w.players[i].Status = "i"
			}
		}
	}})
	ctx := context.Background()

	t.Run("filters by position and sorts by expected points", func(t *testing.T) {
		got, err := r.svc.FindPlayers(ctx, service.PlayerQuery{Position: "GKP", SortBy: "xp", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Name != "Mkt00" {
			t.Fatalf("top goalkeeper = %v, want the 9-a-week Mkt00", names(got))
		}
		for i, p := range got {
			if p.Position != "GKP" || (i > 0 && p.XP > got[i-1].XP) {
				t.Errorf("not all goalkeepers, or not sorted: %+v", got)
			}
		}
	})
	t.Run("price ceiling is in pounds millions", func(t *testing.T) {
		got, err := r.svc.FindPlayers(ctx, service.PlayerQuery{MaxPrice: 5.0, Limit: 25})
		if err != nil || len(got) == 0 {
			t.Fatalf("%v %v", got, err)
		}
		for _, p := range got {
			price, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(p.Price, "£"), "m"), 64)
			if err != nil || price > 5.0 {
				t.Errorf("%s costs %s, over the £5.0m ceiling (parse error: %v)", p.Name, p.Price, err)
			}
		}
	})
	t.Run("injured players are excluded unless asked for", func(t *testing.T) {
		def, _ := r.svc.FindPlayers(ctx, service.PlayerQuery{Limit: 25})
		inj, err := r.svc.FindPlayers(ctx, service.PlayerQuery{Status: "injured", Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range def {
			if p.Name == "Mkt04" {
				t.Error("an injured player was returned by default")
			}
		}
		if len(inj) != 1 || inj[0].Name != "Mkt04" || inj[0].Status != "injured" {
			t.Errorf("injured = %+v", inj)
		}
	})
	t.Run("filters by club short name", func(t *testing.T) {
		got, err := r.svc.FindPlayers(ctx, service.PlayerQuery{Team: "c07", Limit: 25})
		if err != nil || len(got) == 0 {
			t.Fatalf("%v %v", got, err)
		}
		for _, p := range got {
			if p.Team != "C07" {
				t.Errorf("%s plays for %s", p.Name, p.Team)
			}
		}
	})
	t.Run("finds one player by name", func(t *testing.T) {
		got, err := r.svc.FindPlayers(ctx, service.PlayerQuery{Name: "mkt03"})
		if err != nil || len(got) != 1 || got[0].Name != "Mkt03" {
			t.Errorf("%v %v", names(got), err)
		}
	})
	t.Run("limit defaults and is capped", func(t *testing.T) {
		def, _ := r.svc.FindPlayers(ctx, service.PlayerQuery{})
		huge, _ := r.svc.FindPlayers(ctx, service.PlayerQuery{Limit: 10_000})
		if len(def) != 10 || len(huge) != 25 {
			t.Errorf("default %d (want 10), capped %d (want 25)", len(def), len(huge))
		}
	})
	t.Run("bad inputs are errors that say what is allowed", func(t *testing.T) {
		for name, q := range map[string]service.PlayerQuery{
			"unknown position": {Position: "WINGER"},
			"unknown club":     {Team: "ZZZ"},
			"unknown sort":     {SortBy: "vibes"},
			"unknown status":   {Status: "sleepy"},
			"negative price":   {MaxPrice: -1},
		} {
			if _, err := r.svc.FindPlayers(ctx, q); err == nil {
				t.Errorf("%s: want an error", name)
			}
		}
	})
}

func TestSearchNews(t *testing.T) {
	hit := db.Hit{
		Title: "Own05 doubt", Content: "Own05 is a doubt for Saturday.", URL: "https://x/1", Source: "bbc",
		PublishedAt: testNow.Add(-3 * time.Hour), Similarity: 0.61, PlayerIDs: []int{105},
	}
	ctx := context.Background()

	t.Run("embeds the question as a query, filters, and names the players", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.store.hits = []db.Hit{hit}

		got, err := r.svc.SearchNews(ctx, service.NewsQuery{Query: "is Own05 injured?", Player: "Own05", Days: 3, Limit: 5})

		if err != nil {
			t.Fatal(err)
		}
		if len(r.emb.kinds) != 1 || r.emb.kinds[0] != embed.Query {
			t.Errorf("embedding kinds = %v, want one Query: stored passages and questions are embedded differently", r.emb.kinds)
		}
		f := r.store.searchCalls[0]
		if !slices.Equal(f.PlayerIDs, []int{105}) || f.Limit != 5 || !f.Since.Equal(testNow.Add(-72*time.Hour)) {
			t.Errorf("filter = %+v", f)
		}
		if len(got) != 1 || got[0].URL != "https://x/1" || got[0].Age != "3h" || !slices.Equal(got[0].Players, []string{"Own05"}) || got[0].Source != "bbc" {
			t.Errorf("result = %+v", got)
		}
	})
	t.Run("no hits is an empty answer, not an error", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		got, err := r.svc.SearchNews(ctx, service.NewsQuery{Query: "anything"})
		if err != nil || len(got) != 0 {
			t.Errorf("%v %v", got, err)
		}
	})
	t.Run("limit defaults and is capped", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		_, _ = r.svc.SearchNews(ctx, service.NewsQuery{Query: "q"})
		_, _ = r.svc.SearchNews(ctx, service.NewsQuery{Query: "q", Limit: 500})
		if r.store.searchCalls[0].Limit != 5 || r.store.searchCalls[1].Limit != 10 {
			t.Errorf("limits = %d, %d; want 5 by default and 10 at most", r.store.searchCalls[0].Limit, r.store.searchCalls[1].Limit)
		}
	})
	t.Run("without an embedder the tool is unavailable, with a typed error", func(t *testing.T) {
		r := newRig(t, rigOpts{noEmbed: true})
		if _, err := r.svc.SearchNews(ctx, service.NewsQuery{Query: "q"}); !errors.Is(err, service.ErrNewsUnavailable) {
			t.Errorf("err = %v, want ErrNewsUnavailable", err)
		}
	})
	t.Run("bad inputs", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for name, q := range map[string]service.NewsQuery{
			"empty query":    {Query: "  "},
			"unknown player": {Query: "q", Player: "Nobody"},
			"negative days":  {Query: "q", Days: -1},
		} {
			if _, err := r.svc.SearchNews(ctx, q); err == nil {
				t.Errorf("%s: want an error", name)
			}
		}
	})
	t.Run("embedding and store failures surface", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.emb.err = errors.New("voyage down")
		if _, err := r.svc.SearchNews(ctx, service.NewsQuery{Query: "q"}); err == nil || !strings.Contains(err.Error(), "voyage down") {
			t.Errorf("embedding error lost: %v", err)
		}
		r2 := newRig(t, rigOpts{})
		r2.store.searchErr = errors.New("db down")
		if _, err := r2.svc.SearchNews(ctx, service.NewsQuery{Query: "q"}); err == nil || !strings.Contains(err.Error(), "db down") {
			t.Errorf("store error lost: %v", err)
		}
	})
}

// deadlinePassed puts the world where it really was on 24 October: the GW8 deadline has
// gone, GW8 is under way, GW9 is next, and the stored history still ends at GW7.
func deadlinePassed(w *world) {
	for i := range w.events {
		switch w.events[i].ID {
		case 8:
			w.events[i].IsNext, w.events[i].IsCurrent = false, true
		case 9:
			w.events[i].IsNext = true
		}
	}
	// In GW9 club 2 hosts club 1: the opposite of GW8, so the two are told apart.
	w.fixtures = append(w.fixtures, fpl.Fixture{ID: 100, Event: 9, HomeTeam: 2, AwayTeam: 1, HomeDifficulty: 3, AwayDifficulty: 3})
}

func TestTheUpcomingGameweekComesFromFPLsScheduleNotFromHowFarTheStoredDataGoes(t *testing.T) {
	r := newRig(t, rigOpts{mutate: deadlinePassed})
	r.fpl.currentEvent = 8 // the manager's GW8 squad is now public
	ctx := context.Background()

	team, err := r.svc.MyTeam(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lineup, err := r.svc.Lineup(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The data ends at GW7, but GW8 is locked: the decision is for GW9.
	if team.Gameweek != 9 || lineup.Gameweek != 9 {
		t.Errorf("gameweeks %d and %d, want 9: GW8 is already locked", team.Gameweek, lineup.Gameweek)
	}
	if team.AsOf != "the GW8 deadline" {
		t.Errorf("squad_as_of = %q: the public squad is the one that took the pitch in GW8", team.AsOf)
	}
	// Own01's club hosts in GW8 but travels in GW9; the view must show GW9's match.
	if first := team.Starting[0]; first.Name != "Own01" || first.Fixture != "C02 (A)" {
		t.Errorf("first starter %+v, want the GW9 fixture C02 (A), not GW8's C02 (H)", first)
	}
	if hasNote(team.Notes, "gaffer sync") {
		t.Errorf("GW8 has not finished, so no history is missing yet: %v", team.Notes)
	}
}

func TestHistoryThatLagsAFinishedGameweekIsFlagged(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		deadlinePassed(w)
		for i := range w.events {
			if w.events[i].ID == 8 {
				w.events[i].Finished, w.events[i].IsCurrent = true, false
			}
		}
	}})
	r.fpl.currentEvent = 8
	ctx := context.Background()

	team, _ := r.svc.MyTeam(ctx)
	lineup, _ := r.svc.Lineup(ctx)
	tr, _ := r.svc.Transfers(ctx, service.TransfersRequest{})

	// Stored history ends at GW7 but GW8 is over: form and expected points are a week behind.
	for name, notes := range map[string][]string{"team": team.Notes, "lineup": lineup.Notes, "transfers": tr.Notes} {
		if !hasNote(notes, "gaffer sync") || !hasNote(notes, "GW8") {
			t.Errorf("%s notes %v should say the history is behind and to run gaffer sync", name, notes)
		}
	}
}

func TestNoUpcomingGameweekIsAnErrorNotAGuess(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		for i := range w.events {
			w.events[i].Finished, w.events[i].IsNext, w.events[i].IsCurrent = true, false, false
		}
	}})

	if _, err := r.svc.MyTeam(context.Background()); err == nil || !strings.Contains(err.Error(), "upcoming gameweek") {
		t.Errorf("err = %v, want one about there being no upcoming gameweek", err)
	}
}

func TestLineupFormation_ShowsTheChosenFormationAndStillComparesAllWithTheBest(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := context.Background()
	best, err := r.svc.Lineup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if best.BestFormation != best.Formation || best.VsBest != 0 || len(best.AllFormations) != 8 {
		t.Fatalf("best %q shown %q vs %v, %d formations", best.BestFormation, best.Formation, best.VsBest, len(best.AllFormations))
	}
	other := ""
	for _, f := range best.OtherFormations {
		other = f.Formation
	}

	got, err := r.svc.LineupFormation(ctx, other)

	if err != nil {
		t.Fatal(err)
	}
	if got.Formation != other || got.BestFormation != best.Formation {
		t.Errorf("shown %q (asked %q), best %q", got.Formation, other, got.BestFormation)
	}
	if got.VsBest > 0 || (other != best.Formation && got.ExpectedPoints > best.ExpectedPoints+1e-9) {
		t.Errorf("a formation cannot beat the best: %v vs %v (delta %v)", got.ExpectedPoints, best.ExpectedPoints, got.VsBest)
	}
	if len(got.Starting) != 11 || len(got.Bench) != 4 || len(got.AllFormations) != 8 {
		t.Errorf("starting %d bench %d formations %d", len(got.Starting), len(got.Bench), len(got.AllFormations))
	}
	for i, f := range got.AllFormations {
		if f != best.AllFormations[i] {
			t.Errorf("the comparison must not change with the chosen formation: %+v vs %+v", f, best.AllFormations[i])
		}
	}
}

func TestLineupFormation_ABadFormationIsAnInputError(t *testing.T) {
	r := newRig(t, rigOpts{})
	_, err := r.svc.LineupFormation(context.Background(), "9-9-9")
	var in *service.InputError
	if !errors.As(err, &in) {
		t.Errorf("err = %v, want an InputError (a 400)", err)
	}
}
