package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

// A what-if is the manager's own transfer plan, checked and weighed in plain Go: the
// rules (like for like, three per club, the money) and every number come from the same
// code the transfer suggestions use, so a plan replayed here agrees with them, and no
// language model is involved.

const (
	maxWhatIfMoves = 15
	// tossUp is the net gain, in points, within which a plan is not clearly good or bad.
	tossUp = 0.3
	// betterBy is how much more a free alternative must be expected to score, per
	// gameweek, before the plan's pick is called a weaker choice for that slot.
	betterBy = 0.3
	// maxBankedTransfers is the most free transfers FPL lets a manager carry.
	maxBankedTransfers = 5
)

// WhatIfMoveRequest sells one player of the squad and buys another, by player id.
type WhatIfMoveRequest struct {
	Out int `json:"out"`
	In  int `json:"in"`
}

// WhatIfRequest is a transfer plan to evaluate.
type WhatIfRequest struct {
	Moves   []WhatIfMoveRequest `json:"moves"`
	Horizon int                 `json:"horizon"` // gameweeks a gain is counted over; default 4
	Free    *int                `json:"free_transfers"`
}

// WhatIfMoveView is one swap with what each side is expected to do.
type WhatIfMoveView struct {
	Out    PlayerView `json:"out"` // carries sell_price
	In     PlayerView `json:"in"`
	OutRun []string   `json:"out_run"` // fixtures over the horizon, "GW9 BHA (H)"
	InRun  []string   `json:"in_run"`
}

// WhatIfWeekView is the XI's expected points in one gameweek, before and after.
type WhatIfWeekView struct {
	Gameweek int     `json:"gameweek"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
	Delta    float64 `json:"delta"`
}

// WhatIfLineupView is the best XI of a squad for the upcoming gameweek.
type WhatIfLineupView struct {
	Formation      string       `json:"formation"`
	ExpectedPoints float64      `json:"expected_points"`
	Captain        string       `json:"captain"`
	ViceCaptain    string       `json:"vice_captain"`
	Starting       []PlayerView `json:"starting"`
	Bench          []PlayerView `json:"bench"`
}

// WhatIfView is the verdict on a plan.
type WhatIfView struct {
	Gameweek int `json:"gameweek"`
	Horizon  int `json:"horizon_gameweeks"`
	// Valid is false when the plan breaks a rule, and Problems says which: nothing below
	// them is then worked out.
	Valid    bool     `json:"valid"`
	Problems []string `json:"problems"`

	Moves         []WhatIfMoveView `json:"moves"`
	Transfers     int              `json:"transfers"`
	FreeTransfers int              `json:"free_transfers_left"`
	Hit           int              `json:"points_hit"`
	FreeNextWeek  int              `json:"free_transfers_next_week"`
	BankBefore    string           `json:"bank_before"`
	BankAfter     string           `json:"bank_after"`

	GainPerGameweek float64 `json:"expected_gain_per_gameweek"`
	NetPoints       float64 `json:"net_points_over_horizon"` // gain over the horizon minus the hit
	Verdict         string  `json:"verdict"`                 // "worth_it", "toss_up" or "not_worth_it"; empty when not valid

	Insights []string         `json:"insights"`
	Weeks    []WhatIfWeekView `json:"weeks"`
	Before   WhatIfLineupView `json:"before"`
	After    WhatIfLineupView `json:"after"`
	Notes    []string         `json:"notes"`
}

// WhatIf checks a transfer plan against the rules and reports what it would do.
func (s *Service) WhatIf(ctx context.Context, req WhatIfRequest) (WhatIfView, error) {
	horizon := req.Horizon
	switch {
	case horizon < 0 || horizon > maxSquadHorizon:
		return WhatIfView{}, badInput("horizon must be between 1 and %d gameweeks", maxSquadHorizon)
	case horizon == 0:
		horizon = defaultHorizon
	}
	if len(req.Moves) > maxWhatIfMoves {
		return WhatIfView{}, badInput("a plan can have at most %d moves", maxWhatIfMoves)
	}
	for _, m := range req.Moves {
		if m.Out < 1 || m.In < 1 {
			return WhatIfView{}, badInput("each move needs the ids of the player to sell and the player to buy")
		}
	}
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return WhatIfView{}, err
	}
	v := WhatIfView{
		Gameweek: base.next, Horizon: horizon, Problems: []string{}, Moves: []WhatIfMoveView{}, Insights: []string{},
		Weeks: []WhatIfWeekView{}, Notes: joinNotes(base.notes, sq.notes),
		Before: emptyLineup(), After: emptyLineup(), BankBefore: formatMoney(sq.fin.Bank), BankAfter: formatMoney(sq.fin.Bank),
	}
	if len(req.Moves) == 0 {
		v.Insights = append(v.Insights, "Pick a player of yours to sell and a player to buy, and the plan is weighed here.")
		return v, nil
	}

	squad := sq.team.Squad.Players
	owned := make(map[int]fpl.Player, len(squad))
	for _, p := range squad {
		owned[p.ID] = p
	}
	market := make(map[int]fpl.Player, len(base.players))
	for _, p := range base.players {
		market[p.ID] = p
	}

	// Rules that depend only on the moves themselves.
	var pairs []movePair
	seenOut, seenIn := map[int]bool{}, map[int]bool{}
	for _, m := range req.Moves {
		out, ownsOut := owned[m.Out]
		in, knowsIn := market[m.In]
		switch {
		case !ownsOut:
			v.Problems = append(v.Problems, fmt.Sprintf("Player %d is not in your squad, so he cannot be sold.", m.Out))
		case !knowsIn:
			v.Problems = append(v.Problems, fmt.Sprintf("Player %d does not exist.", m.In))
		case seenOut[m.Out]:
			v.Problems = append(v.Problems, fmt.Sprintf("%s is sold twice.", out.WebName))
		case seenIn[m.In]:
			v.Problems = append(v.Problems, fmt.Sprintf("%s is bought twice.", in.WebName))
		case owned[m.In].ID != 0:
			v.Problems = append(v.Problems, fmt.Sprintf("%s is already in your squad.", in.WebName))
		case out.Position != in.Position:
			v.Problems = append(v.Problems, fmt.Sprintf("%s (%s) cannot be replaced by %s (%s): a transfer must be like for like.", out.WebName, out.Position, in.WebName, in.Position))
		default:
			seenOut[m.Out], seenIn[m.In] = true, true
			pairs = append(pairs, movePair{out, in})
		}
	}
	if len(v.Problems) > 0 {
		return v, nil
	}

	// The squad that results, and the rules that depend on all of it.
	after := make([]fpl.Player, 0, len(squad))
	swapped := map[int]fpl.Player{}
	for _, p := range pairs {
		swapped[p.out.ID] = p.in
	}
	for _, p := range squad {
		if in, ok := swapped[p.ID]; ok {
			after = append(after, in)
		} else {
			after = append(after, p)
		}
	}
	perClub := map[int]int{}
	for _, p := range after {
		perClub[p.TeamID]++
	}
	var clubs []int
	for club, n := range perClub {
		if n > optimizer.MaxPerClub {
			clubs = append(clubs, club)
		}
	}
	sort.Ints(clubs)
	for _, club := range clubs {
		v.Problems = append(v.Problems, fmt.Sprintf("You would have %d players from %s: the limit is %d.", perClub[club], base.short[club], optimizer.MaxPerClub))
	}
	bankAfter := sq.fin.Bank
	for _, p := range pairs {
		bankAfter += sq.fin.Sell[p.out.ID] - p.in.NowCost
	}
	if bankAfter < 0 {
		v.Problems = append(v.Problems, fmt.Sprintf("You would be %s over budget: the buys cost more than the sales and your bank cover.", formatMoney(-bankAfter)))
	}
	if len(v.Problems) > 0 {
		return v, nil
	}

	// Money, transfers and hit.
	n := len(pairs)
	free := sq.fin.Free.Next
	if req.Free != nil {
		free = max(*req.Free, 0)
	}
	hit := transfers.HitCost * max(0, n-free)
	v.Valid, v.Transfers, v.FreeTransfers, v.Hit = true, n, free, hit
	v.BankAfter = formatMoney(bankAfter)
	if n < free {
		v.FreeNextWeek = min(free-n+1, maxBankedTransfers)
	} else {
		v.FreeNextWeek = 1
	}

	// What the plan is worth, by the same measure the transfer suggestions use.
	score := base.model.ScoreFrom(base.next, horizon)
	gain := transfers.TeamScore(after, score) - transfers.TeamScore(squad, score)
	v.GainPerGameweek = round1(gain)
	v.NetPoints = round1(float64(horizon)*gain - float64(hit))
	switch {
	case v.NetPoints > tossUp:
		v.Verdict = "worth_it"
	case v.NetPoints < -tossUp:
		v.Verdict = "not_worth_it"
	default:
		v.Verdict = "toss_up"
	}

	// The two lineups, and the XI's points in each gameweek of the horizon.
	nextXP := base.model.ScoreFrom(base.next, 1)
	beforeAdv, err := lineup.Advise(squad, lineup.Options{XP: nextXP})
	if err != nil {
		return WhatIfView{}, err
	}
	afterAdv, err := lineup.Advise(after, lineup.Options{XP: nextXP})
	if err != nil {
		return WhatIfView{}, fmt.Errorf("the resulting squad has no legal lineup: %w", err)
	}
	v.Before, v.After = whatIfLineup(base, beforeAdv), whatIfLineup(base, afterAdv)
	for g := base.next; g < base.next+horizon && g <= lastGameweek; g++ {
		xp := base.model.ScoreFrom(g, 1)
		b, errB := lineup.Advise(squad, lineup.Options{XP: xp})
		a, errA := lineup.Advise(after, lineup.Options{XP: xp})
		if errB != nil || errA != nil {
			continue
		}
		v.Weeks = append(v.Weeks, WhatIfWeekView{Gameweek: g, Before: round1(b.Total), After: round1(a.Total), Delta: round1(a.Total - b.Total)})
	}

	for _, p := range pairs {
		out := base.view(p.out, score(p.out))
		out.SellPrice = formatMoney(sq.fin.Sell[p.out.ID])
		v.Moves = append(v.Moves, WhatIfMoveView{
			Out: out, In: base.view(p.in, score(p.in)),
			OutRun: base.run(p.out, horizon), InRun: base.run(p.in, horizon),
		})
	}
	v.Insights = whatIfInsights(base, v, pairs, squad, after, afterAdv, beforeAdv, sq.fin.Bank, sq.fin.Sell, score)
	return v, nil
}

// lastGameweek is the final gameweek of a season.
const lastGameweek = 38

// movePair is a swap that passed the rules: the player sold and the player bought.
type movePair struct{ out, in fpl.Player }

func emptyLineup() WhatIfLineupView {
	return WhatIfLineupView{Starting: []PlayerView{}, Bench: []PlayerView{}}
}

func whatIfLineup(base *baseSnap, a lineup.Advice) WhatIfLineupView {
	v := WhatIfLineupView{
		Formation: a.Formation, ExpectedPoints: round1(a.Total),
		Captain: a.Captain.Player.WebName, ViceCaptain: a.ViceCaptain.Player.WebName,
		Starting: []PlayerView{}, Bench: []PlayerView{},
	}
	for _, e := range a.Starting {
		v.Starting = append(v.Starting, base.view(e.Player, e.XP))
	}
	for _, e := range a.Bench {
		v.Bench = append(v.Bench, base.view(e.Player, e.XP))
	}
	return v
}

// run lists a player's fixtures over the next horizon gameweeks.
func (b *baseSnap) run(p fpl.Player, horizon int) []string {
	out := []string{}
	for g := b.next; g < b.next+horizon && g <= lastGameweek; g++ {
		out = append(out, fmt.Sprintf("GW%d %s", g, fixtureText(b.fixtures.Opponents(p.TeamID, g), b.short)))
	}
	return out
}

func signedPts(x float64) string {
	if x > 0 {
		return fmt.Sprintf("+%.1f", x)
	}
	return fmt.Sprintf("%.1f", x)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// whatIfInsights states, in plain sentences, what the plan means. Every figure in a
// sentence is the rounded one the view shows next to it.
func whatIfInsights(base *baseSnap, v WhatIfView, pairs []movePair, before, after []fpl.Player, afterAdv, beforeAdv lineup.Advice,
	bank int, sell map[int]int, score func(fpl.Player) float64) []string {

	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	h := v.Horizon
	gws := fmt.Sprintf("%d %s", h, plural(h, "gameweek", "gameweeks"))

	switch v.Verdict {
	case "worth_it":
		add("Worth doing: it adds %s points over %s after the cost of the transfers.", signedPts(v.NetPoints), gws)
	case "not_worth_it":
		add("Not worth it: over %s it loses %s points once the transfers' cost is counted.", gws, signedPts(-v.NetPoints)[1:])
	default:
		add("A toss-up: over %s the gain and the cost roughly cancel (net %s).", gws, signedPts(v.NetPoints))
	}

	if v.Hit == 0 {
		add("It uses %d of your %d free %s, so there is no points hit.", v.Transfers, v.FreeTransfers, plural(v.FreeTransfers, "transfer", "transfers"))
	} else {
		add("You have %d free %s for %d %s: that is a %d-point hit.", v.FreeTransfers, plural(v.FreeTransfers, "transfer", "transfers"), v.Transfers, plural(v.Transfers, "transfer", "transfers"), v.Hit)
	}
	add("Next week you would start with %d free %s.", v.FreeNextWeek, plural(v.FreeNextWeek, "transfer", "transfers"))
	add("Your bank goes from %s to %s.", v.BankBefore, v.BankAfter)

	startsAfter := map[int]bool{}
	for _, e := range afterAdv.Starting {
		startsAfter[e.Player.ID] = true
	}
	startsBefore := map[int]bool{}
	for _, e := range beforeAdv.Starting {
		startsBefore[e.Player.ID] = true
	}
	for _, p := range pairs {
		switch {
		case startsAfter[p.in.ID] && startsBefore[p.out.ID]:
			add("%s would take %s's place in the XI the model would pick.", p.in.WebName, p.out.WebName)
		case startsAfter[p.in.ID]:
			add("%s would start in the XI the model would pick.", p.in.WebName)
		default:
			add("%s would not make the XI the model would pick this week: that move adds depth, not points now.", p.in.WebName)
		}
		if !startsBefore[p.out.ID] {
			add("%s is not in the XI the model would pick, so selling him costs this week's lineup nothing.", p.out.WebName)
		}
	}
	if beforeAdv.Captain.Player.ID != afterAdv.Captain.Player.ID {
		add("Your captain would change from %s to %s.", beforeAdv.Captain.Player.WebName, afterAdv.Captain.Player.WebName)
	}

	for _, p := range pairs {
		if !optimizer.Available(p.in) {
			add("%s is %s: check his news before you buy him.", p.in.WebName, statusText(p.in))
		}
		if !optimizer.Available(p.out) {
			add("%s is %s, so selling him frees a slot you cannot fully use.", p.out.WebName, statusText(p.out))
		}
		add("%s is expected to score %.1f a gameweek over %s, against %.1f for %s.", p.in.WebName, round1(score(p.in)), gws, round1(score(p.out)), p.out.WebName)
	}

	// Is there a clearly better pick for the same slot? Only moves that could actually be
	// made are offered: the right position, affordable, and within the three-per-club limit.
	inSquad := map[int]bool{}
	for _, p := range after {
		inSquad[p.ID] = true
	}
	for _, p := range pairs {
		budget := sell[p.out.ID] + bank
		for _, q := range pairs {
			if q.out.ID != p.out.ID {
				budget += sell[q.out.ID] - q.in.NowCost // the other moves' money is spent or freed already
			}
		}
		clubs := map[int]int{}
		for _, a := range after {
			if a.ID != p.in.ID {
				clubs[a.TeamID]++
			}
		}
		var best *fpl.Player
		bestScore := score(p.in) + betterBy
		for _, c := range base.players {
			c := c
			if c.Position != p.in.Position || inSquad[c.ID] || !optimizer.Available(c) || c.NowCost > budget || clubs[c.TeamID] >= optimizer.MaxPerClub {
				continue
			}
			if s := score(c); s > bestScore || (s == bestScore && best != nil && c.ID < best.ID) {
				best, bestScore = &c, s
			}
		}
		if best != nil {
			add("For %s's slot you could afford %s (%s, %s), expected to score %.1f a gameweek against %.1f for %s.",
				p.out.WebName, best.WebName, base.short[best.TeamID], formatMoney(best.NowCost), round1(score(*best)), round1(score(p.in)), p.in.WebName)
		}
	}
	return out
}
