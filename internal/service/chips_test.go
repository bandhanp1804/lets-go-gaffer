package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func chipsOf(t *testing.T, r *rig) map[string]service.ChipAdviceView {
	t.Helper()
	v, err := r.svc.Chips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Chips) != 4 {
		t.Fatalf("chips = %d", len(v.Chips))
	}
	out := map[string]service.ChipAdviceView{}
	for _, c := range v.Chips {
		out[c.Chip] = c
	}
	return out
}

// doubleForClub1 gives the clubs that the squad's first three players play for a second match in GW9
// (club 1 plays clubs 2 and 3), on top of the usual look-ahead fixtures.
func doubleForClub1(w *world) {
	aheadFixtures(w)
	w.fixtures = append(w.fixtures, fpl.Fixture{ID: 120, Event: 9, HomeTeam: 1, AwayTeam: 3, HomeDifficulty: 3, AwayDifficulty: 3})
}

func TestChips_AllFourAreAdvisedWithAWeekByWeekValue(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	v, err := r.svc.Chips(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if v.FirstGameweek != 8 || v.Gameweeks != 8 {
		t.Errorf("first %d gameweeks %d", v.FirstGameweek, v.Gameweeks)
	}
	want := map[string]string{"3xc": "Triple Captain", "bboost": "Bench Boost", "freehit": "Free Hit", "wildcard": "Wildcard"}
	for _, c := range v.Chips {
		if want[c.Chip] != c.Name || c.Headline == "" || len(c.Insights) == 0 || len(c.Weeks) == 0 || !c.Available {
			t.Errorf("%s: %+v", c.Chip, c)
		}
		for i, w := range c.Weeks {
			if w.Gameweek != 8+i {
				t.Errorf("%s: week %d is GW%d", c.Chip, i, w.Gameweek)
			}
		}
	}
	if len(v.Chips[3].Weeks) != 4 {
		t.Errorf("the Wildcard is tried from the next four gameweeks, got %d", len(v.Chips[3].Weeks))
	}
}

func TestChips_TripleCaptainBelongsInADoubleGameweek(t *testing.T) {
	r := newRig(t, rigOpts{mutate: doubleForClub1})

	tc := chipsOf(t, r)["3xc"]

	if tc.BestGameweek != 9 || tc.Verdict != "wait" {
		t.Fatalf("best GW%d verdict %q: a double should be the week to wait for", tc.BestGameweek, tc.Verdict)
	}
	var gw8, gw9 service.ChipWeekView
	for _, w := range tc.Weeks {
		switch w.Gameweek {
		case 8:
			gw8 = w
		case 9:
			gw9 = w
		}
	}
	if !gw9.Double || gw9.Value <= gw8.Value+1 || tc.ThisWeek != gw8.Value || tc.BestValue != gw9.Value {
		t.Errorf("GW8 %+v GW9 %+v this %v best %v", gw8, gw9, tc.ThisWeek, tc.BestValue)
	}
	if !strings.Contains(tc.Headline, "Wait for GW9") {
		t.Errorf("headline = %q", tc.Headline)
	}
	for _, i := range tc.Insights {
		if strings.Contains(i, "double gameweek in the next") && strings.Contains(i, "None of") {
			t.Errorf("a double exists, so it must not say there is none: %q", i)
		}
	}
}

func TestChips_WithNoDoubleInSightItSaysSo_AndAMarginallyBetterWeekIsNotWorthWaitingFor(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures}) // every match has difficulty 3; no doubles for the squad's clubs

	tc := chipsOf(t, r)["3xc"]

	var says bool
	for _, i := range tc.Insights {
		says = says || strings.Contains(i, "None of your best captain options has a double gameweek")
	}
	if !says {
		t.Errorf("insights = %q", tc.Insights)
	}
	if tc.Verdict == "wait" && tc.BestValue-tc.ThisWeek <= 1.1 {
		t.Errorf("waiting for a week only %.1f better is noise: verdict %q", tc.BestValue-tc.ThisWeek, tc.Verdict)
	}
}

func TestChips_FreeHitIsWorthMostWhereYourPlayersHaveNoMatch(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures}) // in GW10 none of the squad's clubs (1-5) plays

	fh := chipsOf(t, r)["freehit"]

	var blank service.ChipWeekView
	for _, w := range fh.Weeks {
		if w.Gameweek == 10 {
			blank = w
		}
	}
	if fh.BestGameweek != 10 || !strings.Contains(blank.Detail, "15 of your players have no match") || blank.Value <= 0 {
		t.Errorf("best GW%d; GW10 %+v", fh.BestGameweek, blank)
	}
	if fh.Verdict != "wait" || !strings.Contains(fh.Headline, "Wait for GW10") {
		t.Errorf("verdict %q headline %q", fh.Verdict, fh.Headline)
	}
}

func TestChips_BenchBoostCountsTheBenchesExpectedPoints(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})

	bb := chipsOf(t, r)["bboost"]

	var gw10 service.ChipWeekView
	for _, w := range bb.Weeks {
		if w.Gameweek == 10 {
			gw10 = w
		}
	}
	if gw10.Value != 0 || !strings.Contains(gw10.Detail, "0 of your 4 bench players have a match") {
		t.Errorf("when nobody on the bench plays the chip is worth nothing: %+v", gw10)
	}
	if bb.BestValue <= 0 || bb.BestGameweek == 10 {
		t.Errorf("best GW%d (%v)", bb.BestGameweek, bb.BestValue)
	}
}

// The Wildcard figure must be the Transfers page's own Wildcard, or the two would disagree.
func TestChips_WildcardFromNowEqualsTheTransfersPagesWildcard(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	wc := chipsOf(t, r)["wildcard"]
	tr, err := r.svc.Transfers(context.Background(), service.TransfersRequest{Wildcard: true, Horizon: 4})
	if err != nil || len(tr.Suggestions) == 0 {
		t.Fatalf("err %v suggestions %d", err, len(tr.Suggestions))
	}

	if wc.Weeks[0].Value != tr.Suggestions[0].NetPoints || wc.ThisWeek != tr.Suggestions[0].NetPoints {
		t.Errorf("chip planner says %v from now, the Transfers page says %v", wc.Weeks[0].Value, tr.Suggestions[0].NetPoints)
	}
}

func TestChips_ChipsAlreadyPlayedThisHalfAreUnavailable_FromPublicHistory(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	r.fpl.chipsPlayed = []fpl.ChipUse{{Name: "3xc", Event: 3}, {Name: "bboost", Event: 25}} // the second was in the other half

	c := chipsOf(t, r)

	if c["3xc"].Available || c["3xc"].Verdict != "unavailable" || !strings.Contains(strings.Join(c["3xc"].Insights, " "), "no Triple Captain left") {
		t.Errorf("triple captain: %+v", c["3xc"])
	}
	if !c["bboost"].Available || !c["freehit"].Available || !c["wildcard"].Available {
		t.Errorf("a chip from the other half of the season is not used up: %+v", c)
	}
	v, _ := r.svc.Chips(context.Background())
	if !strings.Contains(v.AvailabilityNote, "Triple Captain (GW3)") {
		t.Errorf("the note should say what was counted as played: %q", v.AvailabilityNote)
	}
	if hasNote(v.Notes, "Triple Captain (GW3)") {
		t.Errorf("the availability note belongs in its own field, not with the freshness notes")
	}
}

func TestChips_ALiveSquadReportsItsChipsExactly(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)} // only the wildcard is available
	r := newRig(t, rigOpts{live: live, session: token(testNow.Add(time.Hour)), mutate: aheadFixtures})

	c := chipsOf(t, r)

	if !c["wildcard"].Available || c["3xc"].Available || c["bboost"].Available || c["freehit"].Available {
		t.Errorf("availability: %v %v %v %v", c["wildcard"].Available, c["3xc"].Available, c["bboost"].Available, c["freehit"].Available)
	}
	if r.fpl.historyCalls > 1 {
		t.Errorf("a live squad needs no history to know its chips")
	}
}

func TestChips_AreKeptForAFewMinutes(t *testing.T) {
	r := newRig(t, rigOpts{mutate: aheadFixtures})
	if _, err := r.svc.Chips(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := r.fpl.historyCalls

	if _, err := r.svc.Chips(context.Background()); err != nil {
		t.Fatal(err)
	}

	if r.fpl.historyCalls != calls {
		t.Errorf("the second call recomputed: %d -> %d history reads", calls, r.fpl.historyCalls)
	}
}

func TestChips_NeedATeam(t *testing.T) {
	r := newRig(t, rigOpts{})
	svc := service.New(service.Deps{Store: r.store, FPL: r.fpl, Now: func() time.Time { return testNow }}, service.Config{})

	if _, err := svc.Chips(context.Background()); !errors.Is(err, service.ErrTeamRequired) {
		t.Errorf("err = %v", err)
	}
}
