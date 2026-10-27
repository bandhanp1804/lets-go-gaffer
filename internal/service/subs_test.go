package service_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// The test squad starts slots 1-11 as 4-4-2 (GK 101, DEF 102-105, MID 106-109, FWD 110-111),
// benches GK 112, DEF 113, MID 114, FWD 115 in that order, captains 110 and vice-captains 106.

func subs(t *testing.T, swaps ...service.SubsSwapRequest) service.SubsView {
	t.Helper()
	v, err := newRig(t, rigOpts{}).svc.Subs(context.Background(), service.SubsRequest{Swaps: swaps})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func has(lines []string, part string) bool {
	for _, l := range lines {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

func TestSubs_TheRules(t *testing.T) {
	tests := []struct {
		name    string
		swaps   []service.SubsSwapRequest
		problem string // empty: allowed
	}{
		{"like for like", []service.SubsSwapRequest{{Out: 111, In: 115}}, ""},
		{"a defender off, a midfielder on: 3-5-2", []service.SubsSwapRequest{{Out: 102, In: 114}}, ""},
		{"goalkeeper for goalkeeper", []service.SubsSwapRequest{{Out: 101, In: 112}}, ""},
		{"goalkeeper for an outfielder", []service.SubsSwapRequest{{Out: 101, In: 113}}, "goalkeeper can only swap with a goalkeeper"},
		{"two defenders off: 2-5-3", []service.SubsSwapRequest{{Out: 102, In: 114}, {Out: 103, In: 115}}, "does not allow: a goalkeeper and at least 3 defenders"},
		{"a bench player taken off", []service.SubsSwapRequest{{Out: 113, In: 114}}, "on the bench already"},
		{"a starter brought on", []service.SubsSwapRequest{{Out: 102, In: 103}}, "already starts"},
		{"one player in two swaps", []service.SubsSwapRequest{{Out: 111, In: 115}, {Out: 110, In: 115}}, "more than one swap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := subs(t, tt.swaps...)
			if tt.problem == "" {
				if !v.Valid || len(v.Problems) != 0 {
					t.Fatalf("problems %v", v.Problems)
				}
				return
			}
			if v.Valid || !has(v.Problems, tt.problem) {
				t.Errorf("problems %v, want one saying %q", v.Problems, tt.problem)
			}
			if len(v.Starting) != 0 || len(v.Insights) != 0 {
				t.Error("nothing is worked out for a plan FPL would refuse")
			}
		})
	}
}

func TestSubs_TheAnalysisAddsUp(t *testing.T) {
	v := subs(t, service.SubsSwapRequest{Out: 102, In: 114})

	if v.FormationBefore != "4-4-2" || v.Formation != "3-5-2" || !has(v.Insights, "from 4-4-2 to 3-5-2") {
		t.Errorf("formation %s -> %s, insights %v", v.FormationBefore, v.Formation, v.Insights)
	}
	if len(v.Swaps) != 1 || math.Abs(v.Delta-v.Swaps[0].Delta) > 0.11 {
		t.Errorf("one swap: the XI changes by the swap's own difference: %+v vs %v", v.Swaps, v.Delta)
	}
	if math.Abs(v.After-v.Before-v.Delta) > 0.11 || v.VsBest > 0 || v.Best < v.After-0.05 {
		t.Errorf("before %v after %v delta %v best %v vs best %v", v.Before, v.After, v.Delta, v.Best, v.VsBest)
	}
	// The benched defender takes the bench place of the midfielder who came on.
	if len(v.Starting) != 11 || len(v.Bench) != 4 || v.Bench[2].Name != "Own02" || v.Bench[0].Name != "Own12" {
		t.Errorf("bench %v", names(v.Bench))
	}
	if !has(v.Insights, "FPL brings on Own13 first") {
		t.Errorf("the first outfield bench player: %v", v.Insights)
	}
}

func TestSubs_BenchingTheCaptainMovesTheArmband(t *testing.T) {
	v := subs(t, service.SubsSwapRequest{Out: 110, In: 115})

	if v.CaptainBefore != "Own10" || v.Captain != "Own06" || !has(v.Insights, "the armband goes to Own06") {
		t.Errorf("captain %s -> %s, vice %s, %v", v.CaptainBefore, v.Captain, v.ViceCaptain, v.Insights)
	}
	if v.ViceCaptain == v.Captain || v.ViceCaptain == "" {
		t.Errorf("a different vice-captain: %q", v.ViceCaptain)
	}
}

func TestSubs_NoSwapsIsTheXIAsSet(t *testing.T) {
	v := subs(t)
	if !v.Valid || v.Before != v.After || v.Delta != 0 || !has(v.Insights, "Pick a starter") {
		t.Errorf("%+v", v)
	}
}

func TestSubs_Input(t *testing.T) {
	r := newRig(t, rigOpts{})
	var ie *service.InputError
	_, err := r.svc.Subs(context.Background(), service.SubsRequest{Swaps: []service.SubsSwapRequest{{Out: 999, In: 115}}})
	if !errors.As(err, &ie) {
		t.Errorf("a player not in the squad: %v", err)
	}
	five := make([]service.SubsSwapRequest, 5)
	if _, err := r.svc.Subs(context.Background(), service.SubsRequest{Swaps: five}); !errors.As(err, &ie) {
		t.Errorf("too many swaps: %v", err)
	}
}
