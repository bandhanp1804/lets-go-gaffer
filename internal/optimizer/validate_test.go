package optimizer_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
)

// validSquad is a legal 4-4-2 squad costing £90.0m:
//
//	GKP 1-2, DEF 3-7, MID 8-12, FWD 13-15; team = (id-1)%5 + 1, so each of
//	teams 1-5 has exactly 3 players (the club limit, so one more breaks it).
//
// Starting XI: 1 | 3 4 5 6 | 8 9 10 11 | 13 14. Captain 13, vice 8.
func validSquad() fpl.Squad {
	pos := func(id int) fpl.Position {
		switch {
		case id <= 2:
			return fpl.GKP
		case id <= 7:
			return fpl.DEF
		case id <= 12:
			return fpl.MID
		}
		return fpl.FWD
	}
	var s fpl.Squad
	for id := 1; id <= 15; id++ {
		s.Players = append(s.Players, fpl.Player{ID: id, TeamID: (id-1)%5 + 1, Position: pos(id), NowCost: 60})
	}
	s.Starting = []int{1, 3, 4, 5, 6, 8, 9, 10, 11, 13, 14}
	s.Captain, s.ViceCaptain = 13, 8
	return s
}

// rulesOf extracts the distinct violated rules, sorted, from a Validate error.
func rulesOf(t *testing.T, err error) []optimizer.Rule {
	t.Helper()
	if err == nil {
		return nil
	}
	var ve *optimizer.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %T %v, want *optimizer.ValidationError", err, err)
	}
	var rules []optimizer.Rule
	for _, v := range ve.Violations {
		if v.Detail == "" {
			t.Errorf("violation %q has no detail", v.Rule)
		}
		if !slices.Contains(rules, v.Rule) {
			rules = append(rules, v.Rule)
		}
	}
	slices.Sort(rules)
	return rules
}

func TestValidate(t *testing.T) {
	const (
		size     = optimizer.RuleSquadSize
		counts   = optimizer.RulePositionCounts
		budget   = optimizer.RuleBudget
		club     = optimizer.RuleClubLimit
		dup      = optimizer.RuleDuplicate
		xiSize   = optimizer.RuleStartingSize
		xiNot    = optimizer.RuleStartingNotInSquad
		formatn  = optimizer.RuleFormation
		captain  = optimizer.RuleCaptain
		fullCost = 1000
	)

	tests := []struct {
		name   string
		mutate func(s *fpl.Squad)
		want   []optimizer.Rule // distinct rules, any order
	}{
		{"valid squad", func(s *fpl.Squad) {}, nil},

		// Squad composition.
		{"14 players", func(s *fpl.Squad) { s.Players = s.Players[:14] }, []optimizer.Rule{size, counts}}, // bench FWD removed
		{"16 players", func(s *fpl.Squad) {
			s.Players = append(s.Players, fpl.Player{ID: 16, TeamID: 6, Position: fpl.FWD, NowCost: 40})
		}, []optimizer.Rule{size, counts}},
		{"wrong split 3 GKP 4 DEF", func(s *fpl.Squad) { s.Players[6].Position = fpl.GKP }, []optimizer.Rule{counts}}, // bench DEF 7 -> GKP
		{"duplicate player", func(s *fpl.Squad) { s.Players[14] = s.Players[13] }, []optimizer.Rule{dup, club}},       // two copies of 14; team 4 now has 4
		{"empty squad does not panic", func(s *fpl.Squad) { *s = fpl.Squad{} }, []optimizer.Rule{size, counts, xiSize, formatn, captain}},

		// Budget boundary: exactly £100.0m is legal, one tenth over is not.
		{"exactly at budget", func(s *fpl.Squad) { s.Players[14].NowCost = fullCost - 14*60 }, nil},
		{"one tenth over budget", func(s *fpl.Squad) { s.Players[14].NowCost = fullCost - 14*60 + 1 }, []optimizer.Rule{budget}},

		// Club limit boundary: 3 is legal (validSquad has 3 per club), 4 is not.
		{"4 from one club", func(s *fpl.Squad) { s.Players[14].TeamID = 1 }, []optimizer.Rule{club}}, // bench FWD 15 -> team 1

		// Starting XI.
		{"10 starters", func(s *fpl.Squad) { s.Starting = s.Starting[:10] }, []optimizer.Rule{xiSize}}, // drops FWD 14; captain 13 stays
		{"12 starters", func(s *fpl.Squad) { s.Starting = append(s.Starting, 12) }, []optimizer.Rule{xiSize}},
		{"starter not in squad", func(s *fpl.Squad) { s.Starting[9] = 99 }, []optimizer.Rule{xiNot, captain}}, // replaces captain 13
		{"duplicate starter", func(s *fpl.Squad) { s.Starting[10] = 13 }, []optimizer.Rule{dup}},

		// Formation.
		{"3-4-3 is legal", func(s *fpl.Squad) { s.Starting = []int{1, 3, 4, 5, 8, 9, 10, 11, 13, 14, 15} }, nil},
		{"5-4-1 is legal", func(s *fpl.Squad) { s.Starting = []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13} }, nil},
		{"3-5-2 is legal", func(s *fpl.Squad) { s.Starting = []int{1, 3, 4, 5, 8, 9, 10, 11, 12, 13, 14} }, nil},
		{"two goalkeepers", func(s *fpl.Squad) { s.Starting = []int{1, 2, 3, 4, 5, 8, 9, 10, 11, 13, 14} }, []optimizer.Rule{formatn}},
		{"no goalkeeper", func(s *fpl.Squad) { s.Starting = []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 14} }, []optimizer.Rule{formatn}},
		{"only two defenders", func(s *fpl.Squad) { s.Starting = []int{1, 3, 4, 8, 9, 10, 11, 12, 13, 14, 15} }, []optimizer.Rule{formatn}},
		{"no forward", func(s *fpl.Squad) {
			s.Starting = []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
			s.Captain, s.ViceCaptain = 8, 9 // the usual captain 13 is a forward and not in this XI
		}, []optimizer.Rule{formatn}},

		// Captaincy.
		{"captain not set", func(s *fpl.Squad) { s.Captain = 0 }, []optimizer.Rule{captain}},
		{"vice not set", func(s *fpl.Squad) { s.ViceCaptain = 0 }, []optimizer.Rule{captain}},
		{"captain is vice", func(s *fpl.Squad) { s.ViceCaptain = s.Captain }, []optimizer.Rule{captain}},
		{"captain on the bench", func(s *fpl.Squad) { s.Captain = 15 }, []optimizer.Rule{captain}},
		{"vice on the bench", func(s *fpl.Squad) { s.ViceCaptain = 12 }, []optimizer.Rule{captain}},

		// Independent problems are all reported, not just the first.
		{"over budget and over club limit", func(s *fpl.Squad) {
			s.Players[14].NowCost = 500
			s.Players[14].TeamID = 1
		}, []optimizer.Rule{budget, club}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := validSquad()
			tc.mutate(&s)

			err := optimizer.Validate(s)

			got := rulesOf(t, err)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("rules = %v, want %v\nerr: %v", got, want, err)
			}
			if tc.want == nil && err != nil {
				t.Errorf("valid squad returned error: %v", err)
			}
		})
	}
}

func TestValidate_ErrorMessageNamesTheProblems(t *testing.T) {
	s := validSquad()
	s.Players[14].NowCost = 500 // 840 + 500 = 1340
	s.Players[14].TeamID = 1

	err := optimizer.Validate(s)

	msg := err.Error()
	for _, want := range []string{"budget", "1340", "1000", "club"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
}

func TestValidate_DoesNotMutateTheSquad(t *testing.T) {
	s := validSquad()
	before := validSquad()

	_ = optimizer.Validate(s)

	if !slices.Equal(s.Starting, before.Starting) || !slices.Equal(s.Players, before.Players) {
		t.Error("Validate modified its input")
	}
}

func TestValidateWithBudget(t *testing.T) {
	// A real squad's price can exceed the £100.0m starting budget once prices rise,
	// so a pulled squad must be checkable against its owner's actual funds.
	rich := validSquad()
	for i := range rich.Players {
		rich.Players[i].NowCost = 68 // 15 x 68 = 1020
	}

	if err := optimizer.Validate(rich); err == nil {
		t.Fatal("Validate should reject 1020 against the fixed 1000 budget")
	}
	if err := optimizer.ValidateWithBudget(rich, 1020); err != nil {
		t.Errorf("exactly at a 1020 budget: %v", err)
	}
	if err := optimizer.ValidateWithBudget(rich, 1019); err == nil {
		t.Error("one tenth over a 1019 budget should fail")
	}
	if err := optimizer.ValidateWithBudget(rich, optimizer.NoBudgetLimit); err != nil {
		t.Errorf("no budget limit: %v", err)
	}

	// Skipping the budget never skips the other rules.
	rich.Players[14].TeamID = 1 // a fourth player from club 1
	err := optimizer.ValidateWithBudget(rich, optimizer.NoBudgetLimit)
	if got := rulesOf(t, err); !slices.Equal(got, []optimizer.Rule{optimizer.RuleClubLimit}) {
		t.Errorf("rules = %v, want only the club limit", got)
	}
}
