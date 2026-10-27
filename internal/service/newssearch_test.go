package service_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/db"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// joao makes Own05 (id 105, an arsenal-style squad player) the accented "João Pedro".
func joao(w *world) { rename(w, 105, "João Pedro") }

func search(t *testing.T, r *rig, q service.NewsQuery) service.NewsSearchView {
	t.Helper()
	v, err := r.svc.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSearch_APlayerNamedInTheQuestionNarrowsTheSearchToHim_IgnoringAccentsAndCase(t *testing.T) {
	for _, q := range []string{
		"is joao pedro injured?", "Is JOÃO PEDRO injured", "what about João Pedro's knock", "joao pedro", "joao pedro, injured?",
		"(joao pedro) fit?", "João Pedro’s knock", "is he fit - joao pedro!", "JOAO-PEDRO news", "any news on joao pedro...",
	} {
		r := newRig(t, rigOpts{mutate: joao})

		v := search(t, r, service.NewsQuery{Query: q})

		if got := r.store.searchCalls[0].PlayerIDs; !slices.Equal(got, []int{105}) {
			t.Errorf("%q: filtered to %v, want only João Pedro (105)", q, got)
		}
		if !v.Detected || v.Player == nil || v.Player.Name != "João Pedro" {
			t.Errorf("%q: detected %v player %+v", q, v.Detected, v.Player)
		}
	}
}

// This is the reported case: a stored injury note for someone else matched "injured".
func TestSearch_WhenNothingIsStoredAboutThePlayerItSaysSoWithFPLsOwnWord(t *testing.T) {
	r := newRig(t, rigOpts{mutate: joao})
	// Without the filter the store would return another player's injury note; with it, nothing.
	r.store.hits = nil

	v := search(t, r, service.NewsQuery{Query: "is joao pedro injured?"})

	if len(v.Stories) != 0 {
		t.Fatalf("stories = %+v", v.Stories)
	}
	if !hasNote(v.Notes, "No stored story mentions João Pedro") || !hasNote(v.Notes, "available") {
		t.Errorf("notes = %q: say there is nothing, and what FPL lists him as", v.Notes)
	}
	if v.Player.Status != "available" {
		t.Errorf("player = %+v", v.Player)
	}
}

func TestSearch_IfFPLHasANoteOnHimItIsQuoted(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		joao(w)
		for i := range w.players {
			if w.players[i].ID == 105 {
				w.players[i].Status, w.players[i].News = "i", "Knee injury - Expected back 7 Nov"
			}
		}
	}})

	v := search(t, r, service.NewsQuery{Query: "joao pedro news"})

	if !hasNote(v.Notes, "FPL lists him as injured: Knee injury - Expected back 7 Nov") || v.Player.News == "" {
		t.Errorf("notes = %q player = %+v", v.Notes, v.Player)
	}
}

func TestSearch_AStoryAboutHimIsFoundAndTheNoteSaysWhyItWasNarrowed(t *testing.T) {
	r := newRig(t, rigOpts{mutate: joao})
	r.store.hits = []db.Hit{{Title: "João Pedro doubt", Content: "x", URL: "https://x/jp", Source: "sky", PublishedAt: testNow.Add(-5 * time.Hour), Similarity: 0.55, PlayerIDs: []int{105}}}

	v := search(t, r, service.NewsQuery{Query: "is joao pedro injured?"})

	if len(v.Stories) != 1 || v.Stories[0].Players[0] != "João Pedro" || !hasNote(v.Notes, "who is named in your question") {
		t.Errorf("stories %+v notes %q", v.Stories, v.Notes)
	}
}

func TestSearch_ANameInsideALongerNameIsNotAnotherPlayer(t *testing.T) {
	// "pedro" alone would also fit a player called Pedro; the longer name is the one meant.
	r := newRig(t, rigOpts{mutate: func(w *world) { joao(w); rename(w, 106, "Pedro") }})

	search(t, r, service.NewsQuery{Query: "is joao pedro injured?"})

	if got := r.store.searchCalls[0].PlayerIDs; !slices.Equal(got, []int{105}) {
		t.Errorf("filtered to %v, want João Pedro only", got)
	}
}

func TestSearch_ANameTwoPlayersShareDetectsNobodyAndSaysWhoFits(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) { rename(w, 105, "Silva"); rename(w, 202, "Silva") }})

	v := search(t, r, service.NewsQuery{Query: "is silva injured?"})

	if len(r.store.searchCalls[0].PlayerIDs) != 0 || v.Detected || v.Player != nil {
		t.Errorf("an ambiguous name must not narrow the search: %+v", r.store.searchCalls[0])
	}
	if !hasNote(v.Notes, "More than one player fits that name") || !hasNote(v.Notes, "Silva (C") {
		t.Errorf("notes = %q", v.Notes)
	}
}

func TestSearch_SeveralDifferentPlayersMeanNoNarrowing(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) { rename(w, 105, "Haaland"); rename(w, 106, "Salah") }})

	v := search(t, r, service.NewsQuery{Query: "haaland or salah, who is fitter?"})

	if len(r.store.searchCalls[0].PlayerIDs) != 0 || v.Player != nil || !hasNote(v.Notes, "Several players are named") {
		t.Errorf("filter %+v notes %q", r.store.searchCalls[0], v.Notes)
	}
}

func TestSearch_AnExplicitPlayerWinsAndIsMatchedIgnoringAccents(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) { joao(w); rename(w, 106, "Salah") }})

	v := search(t, r, service.NewsQuery{Query: "salah", Player: "joao pedro"})

	if got := r.store.searchCalls[0].PlayerIDs; !slices.Equal(got, []int{105}) || v.Detected {
		t.Errorf("filtered to %v detected %v: a player given in the box is used, and is not 'detected'", got, v.Detected)
	}
}

func TestSearch_DetectionCanBeTurnedOff(t *testing.T) {
	r := newRig(t, rigOpts{mutate: joao})

	v := search(t, r, service.NewsQuery{Query: "is joao pedro injured?", NoDetect: true})

	if len(r.store.searchCalls[0].PlayerIDs) != 0 || v.Player != nil {
		t.Errorf("filter %+v player %+v", r.store.searchCalls[0], v.Player)
	}
}

func TestSearch_AQuestionWithNoPlayerSearchesEverythingAndSaysWhenTheMatchesAreLoose(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.store.hits = []db.Hit{{Title: "Ten Hag on data", Content: "x", URL: "https://x/1", Source: "sky", PublishedAt: testNow.Add(-3 * time.Hour), Similarity: 0.34}}

	loose := search(t, r, service.NewsQuery{Query: "manager press conference"})

	if len(r.store.searchCalls[0].PlayerIDs) != 0 || !hasNote(loose.Notes, "loose matches") || !hasNote(loose.Notes, "0.34") {
		t.Errorf("filter %+v notes %q", r.store.searchCalls[0], loose.Notes)
	}

	r.store.hits[0].Similarity = 0.55
	good := search(t, r, service.NewsQuery{Query: "manager press conference"})
	if hasNote(good.Notes, "loose") {
		t.Errorf("a good match must not be called loose: %q", good.Notes)
	}
}

func TestSearch_SaysHowOldTheStoredNewsIsAndWhenItIsStale(t *testing.T) {
	fresh := newRig(t, rigOpts{}) // the fake store's newest story is two hours old
	v := search(t, fresh, service.NewsQuery{Query: "anything"})
	if v.NewestAge != "2h" || hasNote(v.Notes, "Run `gaffer news`") {
		t.Errorf("fresh: age %q notes %q", v.NewestAge, v.Notes)
	}

	stale := newRig(t, rigOpts{})
	stale.store.newest = testNow.Add(-72 * time.Hour)
	old := search(t, stale, service.NewsQuery{Query: "anything"})
	if old.NewestAge != "3d" || !hasNote(old.Notes, "newest stored story is 3d old") || !hasNote(old.Notes, "gaffer news") {
		t.Errorf("stale: age %q notes %q", old.NewestAge, old.Notes)
	}
}

func TestSearch_LinkTextThatWasStoredAsAStoryIsLeftOut(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.store.hits = []db.Hit{
		{Title: "Get in touch here", Content: "x", URL: "https://x/1", Source: "bbc", PublishedAt: testNow.Add(-3 * time.Hour), Similarity: 0.4},
		{Title: "Spurs lack confidence", Content: "x", URL: "https://x/2", Source: "bbc", PublishedAt: testNow.Add(-3 * time.Hour), Similarity: 0.3},
	}

	v := search(t, r, service.NewsQuery{Query: "spurs"})

	if len(v.Stories) != 1 || v.Stories[0].Title != "Spurs lack confidence" {
		t.Errorf("stories = %+v", v.Stories)
	}
}

func TestSearch_TheAssistantsSearchNewsGetsTheSameNarrowing(t *testing.T) {
	r := newRig(t, rigOpts{mutate: joao})

	if _, err := r.svc.SearchNews(context.Background(), service.NewsQuery{Query: "is joao pedro injured?"}); err != nil {
		t.Fatal(err)
	}

	if got := r.store.searchCalls[0].PlayerIDs; !slices.Equal(got, []int{105}) {
		t.Errorf("the assistant's tool must not return another player's injury note: %v", got)
	}
}

func TestStatus_ReportsHowOldTheNewestStoryIs(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.store.newest = testNow.Add(-50 * time.Hour)

	got, err := r.svc.Status(context.Background())

	if err != nil || got.NewsNewestAge != "2d" {
		t.Errorf("err %v age %q", err, got.NewsNewestAge)
	}
}

func TestFold(t *testing.T) {
	// fold is internal to the package, so it is exercised through the player matching it serves.
	r := newRig(t, rigOpts{mutate: func(w *world) { rename(w, 105, "N.Groß-Müller"); rename(w, 106, "O'Brien") }})
	v := search(t, r, service.NewsQuery{Query: "any news on obrien, please?"})
	if v.Player == nil || v.Player.Name != "O'Brien" {
		t.Errorf("an apostrophe inside a name must not stop the match: %+v", v.Player)
	}
	v = search(t, r, service.NewsQuery{Query: "is n gross muller ok?"})
	if v.Player == nil || v.Player.Name != "N.Groß-Müller" {
		t.Errorf("punctuation, ß and ü inside a name: %+v", v.Player)
	}
}

// Injuries, prices and form come from the stored players, so every view built on them is only as fresh.
func TestPlayerData_ANoteSaysHowOldItIsOnceItIsOverADay(t *testing.T) {
	fresh := newRig(t, rigOpts{})
	v, err := fresh.svc.MyTeam(context.Background())
	if err != nil || hasNote(v.Notes, "Player data") {
		t.Fatalf("an hour-old sync needs no note: %v %q", err, v.Notes)
	}

	stale := newRig(t, rigOpts{})
	stale.store.playersAt = testNow.Add(-75 * time.Hour)
	for name, notes := range map[string]func() []string{
		"my team": func() []string { v, _ := stale.svc.MyTeam(context.Background()); return v.Notes },
		"lineup":  func() []string { v, _ := stale.svc.Lineup(context.Background()); return v.Notes },
	} {
		if n := notes(); !hasNote(n, "Player data (injuries, prices, form) was last updated 3d ago") || !hasNote(n, "gaffer sync") {
			t.Errorf("%s: notes = %q", name, n)
		}
	}
}

func TestPlayerData_StatusReportsItsAge(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.store.playersAt = testNow.Add(-30 * time.Hour)

	got, err := r.svc.Status(context.Background())

	if err != nil || got.PlayersAge != "30h" {
		t.Errorf("err %v age %q", err, got.PlayersAge)
	}
}

func TestPlayerData_NoPlayersYetMeansNoNoteAndNoAge(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.store.playersAt = time.Time{}

	got, err := r.svc.Status(context.Background())

	if err != nil || got.PlayersAge != "" || hasNote(got.Notes, "Player data") {
		t.Errorf("err %v age %q notes %q", err, got.PlayersAge, got.Notes)
	}
}

// FPL shows "Palmer", but people type "Cole Palmer" in the Player box.
func TestPlayerBox_AcceptsTheFullNameAsWellAsTheWebName(t *testing.T) {
	r := newRig(t, rigOpts{mutate: func(w *world) {
		for i := range w.players {
			if w.players[i].ID == 105 {
				w.players[i].WebName, w.players[i].FirstName, w.players[i].LastName = "Palmer", "Cole", "Palmer"
			}
		}
	}})
	tests := []struct {
		player  string
		wantErr bool
	}{
		{"Palmer", false},
		{"cole palmer", false},
		{"Cole  PALMER", false},
		{"pal", false}, // part of a web name, as before
		{"cole", true}, // part of a first name alone is not enough
		{"palmer cole", true},
		{"cole smith", true},
	}
	for _, tt := range tests {
		v, err := r.svc.Search(context.Background(), service.NewsQuery{Query: "is he suspended?", Player: tt.player})
		if tt.wantErr {
			if err == nil {
				t.Errorf("Player %q: want an error, got %+v", tt.player, v.Player)
			}
			continue
		}
		if err != nil || v.Player == nil || v.Player.Name != "Palmer" {
			t.Errorf("Player %q: %+v, %v", tt.player, v.Player, err)
		}
	}
}

// A key on the free tier allows 3 embeddings a minute: a question asked again must not spend one.
func TestSearch_AQuestionAskedAgainIsNotEmbeddedAgain(t *testing.T) {
	r := newRig(t, rigOpts{})
	ask := func(q string) { search(t, r, service.NewsQuery{Query: q, NoDetect: true}) }

	ask("is saka injured?")
	ask("is saka injured?")
	ask("  Is SAKA   injured? ") // the same question, typed differently
	if got := len(r.emb.calls); got != 1 {
		t.Fatalf("embedded %d times, want once", got)
	}

	ask("who scores most for arsenal?")
	if got := len(r.emb.calls); got != 2 {
		t.Errorf("a new question embedded %d times in all, want 2", got)
	}
}
