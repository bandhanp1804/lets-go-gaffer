package service_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

func lineupWith(t *testing.T, r *rig, req service.LineupRequest) service.LineupView {
	t.Helper()
	v, err := r.svc.LineupWith(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCaptainChoice_ChoosingTheRecommendedCaptainChangesNothing(t *testing.T) {
	r := newRig(t, rigOpts{})
	best := lineupWith(t, r, service.LineupRequest{})

	same := lineupWith(t, r, service.LineupRequest{Captain: best.CaptainID})

	if best.CaptainChosen || best.CaptainCost != 0 || best.RecommendedCaptain != best.Captain {
		t.Errorf("the default is the recommendation: chosen %v cost %v rec %q", best.CaptainChosen, best.CaptainCost, best.RecommendedCaptain)
	}
	if !same.CaptainChosen || same.CaptainCost != 0 || same.ExpectedPoints != best.ExpectedPoints {
		t.Errorf("picking the same captain: chosen %v cost %v xp %v vs %v", same.CaptainChosen, same.CaptainCost, same.ExpectedPoints, best.ExpectedPoints)
	}
}

func TestCaptainChoice_AWorseCaptainCostsExactlyTheDifferenceInTheDouble(t *testing.T) {
	r := newRig(t, rigOpts{})
	best := lineupWith(t, r, service.LineupRequest{})
	var worse service.CaptainOptionView
	for _, o := range best.CaptainOptions {
		if o.Cost > 0 {
			worse = o // the last one with a cost is the weakest starter
		}
	}
	if worse.ID == 0 {
		t.Fatal("the test squad should have starters of different strength")
	}

	got := lineupWith(t, r, service.LineupRequest{Captain: worse.ID})

	if got.CaptainID != worse.ID || got.Captain != worse.Name || !got.CaptainChosen || got.RecommendedCaptain != best.Captain {
		t.Errorf("captain %q (%d), chosen %v, recommended %q", got.Captain, got.CaptainID, got.CaptainChosen, got.RecommendedCaptain)
	}
	if got.CaptainCost != worse.Cost || math.Abs((best.ExpectedPoints-got.ExpectedPoints)-got.CaptainCost) > 0.11 {
		t.Errorf("cost %v, option said %v, points %v -> %v", got.CaptainCost, worse.Cost, best.ExpectedPoints, got.ExpectedPoints)
	}
	if got.ExpectedPoints >= best.ExpectedPoints {
		t.Errorf("a worse captain cannot raise the total: %v vs %v", got.ExpectedPoints, best.ExpectedPoints)
	}
	for i, p := range got.Starting {
		if p.ID != best.Starting[i].ID {
			t.Errorf("the captain choice must not change who starts")
		}
	}
	if got.ViceCaptainID == got.CaptainID || got.ViceCaptain == "" {
		t.Errorf("vice %q (%d)", got.ViceCaptain, got.ViceCaptainID)
	}
}

func TestCaptainChoice_TheOptionsAreTheStartersBestFirstWithNumbersThatMultiplyOut(t *testing.T) {
	r := newRig(t, rigOpts{})

	v := lineupWith(t, r, service.LineupRequest{})

	if len(v.CaptainOptions) != 11 {
		t.Fatalf("options = %d", len(v.CaptainOptions))
	}
	if v.CaptainOptions[0].Cost != 0 || !v.CaptainOptions[0].Captain || v.CaptainOptions[0].Name != v.Captain {
		t.Errorf("the best option leads: %+v", v.CaptainOptions[0])
	}
	flags := 0
	for i, o := range v.CaptainOptions {
		if i > 0 && o.XP > v.CaptainOptions[i-1].XP {
			t.Errorf("not sorted at %d", i)
		}
		if o.AsCaptain != math.Round(2*o.XP*10)/10 || o.Cost != math.Round((v.CaptainOptions[0].XP-o.XP)*10)/10 {
			t.Errorf("%s: xp %v as captain %v cost %v must follow from what is shown", o.Name, o.XP, o.AsCaptain, o.Cost)
		}
		if o.Captain || o.Vice {
			flags++
		}
	}
	if flags != 2 {
		t.Errorf("exactly the captain and the vice are flagged, got %d", flags)
	}
}

func TestCaptainChoice_ABenchPlayerOrAStrangerOrTheSameTwiceIsAnInputErrorThatNamesWhy(t *testing.T) {
	r := newRig(t, rigOpts{})
	best := lineupWith(t, r, service.LineupRequest{})
	var in *service.InputError
	tests := []struct {
		name string
		req  service.LineupRequest
		want string
	}{
		{"bench player as captain", service.LineupRequest{Captain: best.Bench[0].ID}, "cannot be captain"},
		{"bench player as vice", service.LineupRequest{Vice: best.Bench[0].ID}, "cannot be vice-captain"},
		{"not in the squad", service.LineupRequest{Captain: strongGK}, "not in your squad"},
		{"the same player twice", service.LineupRequest{Captain: best.CaptainID, Vice: best.CaptainID}, "different players"},
	}
	for _, tt := range tests {
		_, err := r.svc.LineupWith(context.Background(), tt.req)
		if !errors.As(err, &in) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want an InputError mentioning %q", tt.name, err, tt.want)
		}
	}
	if _, err := r.svc.LineupWith(context.Background(), service.LineupRequest{Captain: best.Bench[0].ID}); err != nil && !strings.Contains(err.Error(), best.Bench[0].Name) {
		t.Errorf("the error should name him: %v", err)
	}
}

func TestCaptainChoice_ACaptainMustStartInTheChosenFormation(t *testing.T) {
	r := newRig(t, rigOpts{})
	best := lineupWith(t, r, service.LineupRequest{})
	// A bench player in the best formation may start in another; picking him there must work.
	for _, f := range best.OtherFormations {
		other := lineupWith(t, r, service.LineupRequest{Formation: f.Formation})
		for _, b := range best.Bench {
			started := false
			for _, p := range other.Starting {
				started = started || p.ID == b.ID
			}
			if started {
				got := lineupWith(t, r, service.LineupRequest{Formation: f.Formation, Captain: b.ID})
				if got.CaptainID != b.ID || got.Formation != f.Formation {
					t.Errorf("%s: captain %d formation %s", f.Formation, got.CaptainID, got.Formation)
				}
				return
			}
		}
	}
	t.Skip("no formation in the test squad lets a bench player start")
}

func TestCaptainChoice_TheTripleCaptainCardFollowsTheChosenCaptain(t *testing.T) {
	r := newRig(t, rigOpts{mutate: doubleForClub1})
	free, err := r.svc.Chips(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Own02 plays for club 2, which has no double in GW9 (only club 1 does).
	got, err := r.svc.ChipsFor(context.Background(), service.ChipsRequest{Captain: 102})
	if err != nil {
		t.Fatal(err)
	}

	tcFree, tcOwn := free.Chips[0], got.Chips[0]
	if tcOwn.Chip != "3xc" || !strings.HasPrefix(tcOwn.Weeks[0].Detail, "Own02") {
		t.Errorf("every week must be about the chosen captain: %+v", tcOwn.Weeks[0])
	}
	if len(tcOwn.Insights) == 0 || !strings.Contains(tcOwn.Insights[0], "Shown for Own02") {
		t.Errorf("insights = %q", tcOwn.Insights)
	}
	if tcOwn.BestValue > tcFree.BestValue+0.01 {
		t.Errorf("one chosen player cannot beat the best option each week: %v vs %v", tcOwn.BestValue, tcFree.BestValue)
	}
	for i := 1; i < 4; i++ {
		if got.Chips[i].Headline != free.Chips[i].Headline {
			t.Errorf("only the Triple Captain card follows the captain: %s changed", got.Chips[i].Name)
		}
	}
	again, err := r.svc.Chips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Chips[0].Weeks[0].Detail != tcFree.Weeks[0].Detail || again.Chips[0].BestGameweek != tcFree.BestGameweek {
		t.Errorf("a request for one captain must not change what is kept for the next request: %+v vs %+v", again.Chips[0].Weeks[0], tcFree.Weeks[0])
	}
}

func TestCaptainChoice_ChipsForAStrangerIsAnInputError(t *testing.T) {
	r := newRig(t, rigOpts{})
	var in *service.InputError
	if _, err := r.svc.ChipsFor(context.Background(), service.ChipsRequest{Captain: strongGK}); !errors.As(err, &in) {
		t.Errorf("err = %v", err)
	}
}
