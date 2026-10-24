package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/lineup"
	"github.com/bandhanpatel/lets-go-gaffer/internal/optimizer"
	"github.com/bandhanpatel/lets-go-gaffer/internal/transfers"
)

// The chip planner answers "when should I play it?" from the fixture list and the model's expected
// points, week by week, with the same exact solvers the rest of the app uses. It states numbers and
// a recommendation; it does not know a manager's hunches about a particular week.

const (
	chipHorizon       = 8 // gameweeks looked at
	wildcardWindow    = 4 // how far a Wildcard's gain is counted
	wildcardStarts    = 4 // how many weeks it is tried from
	firstHalfLastWeek = 19
	chipsCacheTTL     = snapshotTTL
	relativeTolerance = 0.15
	chipTripleCaptain = "3xc"
	chipBenchBoost    = "bboost"
	chipFreeHit       = "freehit"
	chipWildcard      = "wildcard"
)

// ChipWeekView is one gameweek's value of a chip.
type ChipWeekView struct {
	Gameweek int     `json:"gameweek"`
	Value    float64 `json:"value"`  // the chip's expected gain that week, in points
	Detail   string  `json:"detail"` // what that figure is made of
	Double   bool    `json:"double"` // a player the figure depends on has two matches
}

// ChipAdviceView is the verdict on one chip.
type ChipAdviceView struct {
	Chip         string         `json:"chip"` // FPL's name: 3xc, bboost, freehit, wildcard
	Name         string         `json:"name"` // "Triple Captain"
	Available    bool           `json:"available"`
	Verdict      string         `json:"verdict"` // "good_week_now", "wait", "no_clear_week" or "unavailable"
	BestGameweek int            `json:"best_gameweek"`
	BestValue    float64        `json:"best_value"`
	ThisWeek     float64        `json:"this_week_value"`
	Headline     string         `json:"headline"`
	Insights     []string       `json:"insights"`
	Weeks        []ChipWeekView `json:"weeks"`
}

// ChipsView is the chip planner for the manager's squad.
type ChipsView struct {
	FirstGameweek int              `json:"first_gameweek"`
	Gameweeks     int              `json:"gameweeks"`
	Chips         []ChipAdviceView `json:"chips"`
	Notes         []string         `json:"notes"` // how fresh the squad data is, for a reader that has not seen it
	// AvailabilityNote is how "which chips are left" was worked out, when it was not exact. The page
	// shows this and not Notes, which it already shows with the squad.
	AvailabilityNote string `json:"availability_note,omitempty"`
}

var chipNames = map[string]string{chipTripleCaptain: "Triple Captain", chipBenchBoost: "Bench Boost", chipFreeHit: "Free Hit", chipWildcard: "Wildcard"}

// ChipsRequest tunes the planner.
type ChipsRequest struct {
	// Captain, a player id from the squad, makes the Triple Captain card about that player instead of
	// whoever the best option is each week.
	Captain int
}

// Chips plans the four chips. Free Hit and Wildcard solve for the best possible squad, so the
// result is kept for a few minutes.
func (s *Service) Chips(ctx context.Context) (ChipsView, error) {
	return s.ChipsFor(ctx, ChipsRequest{})
}

// ChipsFor is Chips for a chosen captain.
func (s *Service) ChipsFor(ctx context.Context, req ChipsRequest) (ChipsView, error) {
	team, token, err := s.who(ctx)
	if err != nil {
		return ChipsView{}, err
	}
	base, sq, err := s.squadSnapshot(ctx)
	if err != nil {
		return ChipsView{}, err
	}
	e := s.entryFor(newSquadKey(team, token))
	e.cmu.Lock()
	defer e.cmu.Unlock()
	if e.chips == nil || s.deps.Now().Sub(e.chipsAt) >= chipsCacheTTL {
		v, err := s.planChips(ctx, base, sq, team)
		if err != nil {
			return ChipsView{}, err
		}
		e.chips, e.chipsAt = &v, s.deps.Now()
	}
	v := *e.chips
	if req.Captain == 0 {
		return v, nil
	}

	// The Triple Captain card for a chosen captain: only that player's weeks, so it is cheap to redo.
	var chosen *fpl.Player
	for i, p := range sq.team.Squad.Players {
		if p.ID == req.Captain {
			chosen = &sq.team.Squad.Players[i]
		}
	}
	if chosen == nil {
		return ChipsView{}, badInput("player %d is not in your squad", req.Captain)
	}
	v.Chips = append([]ChipAdviceView(nil), v.Chips...)
	for i, c := range v.Chips {
		if c.Chip != chipTripleCaptain {
			continue
		}
		tc := tripleCaptainAdvice(base, sq.team.Squad.Players, v.FirstGameweek+v.Gameweeks-1, chosen)
		tc.Name, tc.Available = c.Name, c.Available
		if !tc.Available {
			tc.Verdict = "unavailable"
			tc.Insights = append([]string{fmt.Sprintf("You have no %s left to play, so this is for reference.", tc.Name)}, tc.Insights...)
		}
		v.Chips[i] = tc
	}
	return v, nil
}

// planChips works out all four chips for the squad.
func (s *Service) planChips(ctx context.Context, base *baseSnap, sq *squadSnap, team int) (ChipsView, error) {

	avail, availNote := s.availableChips(ctx, team, base.next, sq)
	last := min(base.next+chipHorizon-1, lastGameweek)
	v := ChipsView{FirstGameweek: base.next, Gameweeks: last - base.next + 1, Notes: joinNotes(base.notes, sq.notes), Chips: []ChipAdviceView{}}
	v.AvailabilityNote = availNote
	squad := sq.team.Squad.Players

	tc := tripleCaptainAdvice(base, squad, last, nil)
	bb, err := benchBoostAdvice(base, squad, last)
	if err != nil {
		return ChipsView{}, err
	}
	fh, err := freeHitAdvice(base, sq, last)
	if err != nil {
		return ChipsView{}, err
	}
	wc, err := wildcardAdvice(base, sq, last)
	if err != nil {
		return ChipsView{}, err
	}
	for _, a := range []ChipAdviceView{tc, bb, fh, wc} {
		a.Name = chipNames[a.Chip]
		a.Available = avail[a.Chip]
		if !a.Available {
			a.Verdict = "unavailable"
			a.Insights = append([]string{fmt.Sprintf("You have no %s left to play, so this is for reference.", a.Name)}, a.Insights...)
		}
		v.Chips = append(v.Chips, a)
	}
	return v, nil
}

// availableChips says which chips the manager can still play. A live squad reports them exactly. From
// public data they are worked out from the chips already played in the same half of the season, because
// FPL gives a fresh set of chips for the second half.
func (s *Service) availableChips(ctx context.Context, team, nextGW int, sq *squadSnap) (map[string]bool, string) {
	all := map[string]bool{chipTripleCaptain: true, chipBenchBoost: true, chipFreeHit: true, chipWildcard: true}
	if sq.live {
		got := map[string]bool{}
		for _, c := range sq.fin.AvailableChips {
			got[c] = true
		}
		return got, ""
	}
	h, err := s.deps.FPL.FetchHistory(ctx, team)
	if err != nil {
		return all, "Which chips you have left could not be read, so all four are shown as available: check the Chips page on FPL."
	}
	half := func(gw int) int {
		if gw <= firstHalfLastWeek {
			return 1
		}
		return 2
	}
	var played []string
	for _, c := range h.Chips {
		if half(c.Event) == half(nextGW) {
			all[c.Name] = false
			played = append(played, fmt.Sprintf("%s (GW%d)", chipNames[c.Name], c.Event))
		}
	}
	note := "Which chips you have left is worked out from the chips you have played this half of the season, assuming FPL gives a fresh set after GW19; log in (Load my team, Live squad) for the exact list."
	if len(played) > 0 {
		note += " Played: " + strings.Join(played, ", ") + "."
	}
	return all, note
}

// weeklyBest ranks weeks by value, best first, earlier week first on a tie.
func weeklyBest(weeks []ChipWeekView) []ChipWeekView {
	r := append([]ChipWeekView(nil), weeks...)
	sort.SliceStable(r, func(i, j int) bool { return r[i].Value > r[j].Value })
	return r
}

// verdictFor turns the weekly values into a recommendation: play now when this week is the best or
// within a toss-up of it, wait when a later week is clearly better, and say so when no week stands out.
func verdictFor(weeks []ChipWeekView, nextGW int, floor float64) (verdict string, best ChipWeekView, now float64) {
	ranked := weeklyBest(weeks)
	if len(ranked) == 0 {
		return "no_clear_week", ChipWeekView{}, 0
	}
	best = ranked[0]
	for _, w := range weeks {
		if w.Gameweek == nextGW {
			now = w.Value
		}
	}
	// This week is as good as the best when it is within a toss-up of it, or within a sixth of it:
	// 7.7 against 6.7 is noise, 12.1 against 6.7 is not.
	tolerance := math.Max(tossUp, best.Value*relativeTolerance)
	switch {
	case best.Value <= floor:
		return "no_clear_week", best, now
	case best.Value-now <= tolerance:
		return "good_week_now", best, now
	}
	return "wait", best, now
}

func fixturesIn(base *baseSnap, p fpl.Player, gw int) []fpl.Opponent {
	return base.fixtures.Opponents(p.TeamID, gw)
}

func runText(base *baseSnap, p fpl.Player, gw int) string {
	return fixtureText(fixturesIn(base, p, gw), base.short)
}

// ---- Triple Captain: the best captain option's expected points that week is exactly what the chip adds.

func tripleCaptainAdvice(base *baseSnap, squad []fpl.Player, last int, chosen *fpl.Player) ChipAdviceView {
	a := ChipAdviceView{Chip: chipTripleCaptain, Insights: []string{}, Weeks: []ChipWeekView{}}
	candidates := squad
	if chosen != nil {
		candidates = []fpl.Player{*chosen} // the manager has chosen who captains
	}
	for gw := base.next; gw <= last; gw++ {
		xp := base.model.ScoreFrom(gw, 1)
		var top fpl.Player
		topXP := -1.0
		for _, p := range candidates {
			if x := round1(xp(p)); x > topXP || (x == topXP && p.ID < top.ID) {
				top, topXP = p, x
			}
		}
		n := len(fixturesIn(base, top, gw))
		a.Weeks = append(a.Weeks, ChipWeekView{Gameweek: gw, Value: topXP, Double: n > 1,
			Detail: fmt.Sprintf("%s (%s) %s", top.WebName, base.short[top.TeamID], runText(base, top, gw))})
	}
	verdict, best, now := verdictFor(a.Weeks, base.next, 0)
	a.Verdict, a.BestGameweek, a.BestValue, a.ThisWeek = verdict, best.Gameweek, best.Value, now
	ranked := weeklyBest(a.Weeks)
	switch verdict {
	case "good_week_now":
		a.Headline = fmt.Sprintf("This week is as good as any of the next %d: %s would add %.1f with Triple Captain, against %.1f at best (GW%d).", len(a.Weeks), strings.SplitN(a.Weeks[0].Detail, " (", 2)[0], now, best.Value, best.Gameweek)
	case "wait":
		a.Headline = fmt.Sprintf("Wait for GW%d: %s, where Triple Captain adds %.1f, against %.1f this week.", best.Gameweek, best.Detail, best.Value, now)
	default:
		a.Headline = "No gameweek in the next stretch stands out for a Triple Captain."
	}
	for i, w := range ranked {
		if i == 3 {
			break
		}
		tag := ""
		if w.Double {
			tag = " (double gameweek)"
		}
		a.Insights = append(a.Insights, fmt.Sprintf("GW%d: %s, adds %.1f%s.", w.Gameweek, w.Detail, w.Value, tag))
	}
	if len(ranked) > 1 && ranked[0].Value-ranked[1].Value <= tossUp {
		a.Insights = append(a.Insights, fmt.Sprintf("GW%d and GW%d are a toss-up, %.1f against %.1f.", ranked[0].Gameweek, ranked[1].Gameweek, ranked[0].Value, ranked[1].Value))
	}
	anyDouble := false
	for _, w := range a.Weeks {
		anyDouble = anyDouble || w.Double
	}
	who, noDouble := "your best captain options", "None of your best captain options has a double gameweek"
	if chosen != nil {
		who, noDouble = chosen.WebName, chosen.WebName+" has no double gameweek"
	}
	if !anyDouble && verdict != "wait" {
		a.Headline += " No double gameweek is in sight for " + who + "."
	}
	if !anyDouble {
		a.Insights = append(a.Insights, fmt.Sprintf("%s in the next %d gameweeks, and a double is when the chip adds the most.", noDouble, len(a.Weeks)))
	}
	if chosen != nil {
		a.Insights = append([]string{fmt.Sprintf("Shown for %s, whom you have chosen as captain.", chosen.WebName)}, a.Insights...)
	} else {
		a.Insights = append(a.Insights, "The captain is chosen from your current squad: transfers you make before then can change the best week.")
	}
	return a
}

// ---- Bench Boost: the four bench players' expected points count that week.

func benchBoostAdvice(base *baseSnap, squad []fpl.Player, last int) (ChipAdviceView, error) {
	a := ChipAdviceView{Chip: chipBenchBoost, Insights: []string{}, Weeks: []ChipWeekView{}}
	for gw := base.next; gw <= last; gw++ {
		xp := base.model.ScoreFrom(gw, 1)
		adv, err := lineup.Advise(squad, lineup.Options{XP: xp})
		if err != nil {
			return a, err
		}
		var total float64
		playing, doubles := 0, 0
		for _, b := range adv.Bench {
			total += b.XP
			n := len(fixturesIn(base, b.Player, gw))
			if n > 0 {
				playing++
			}
			if n > 1 {
				doubles++
			}
		}
		a.Weeks = append(a.Weeks, ChipWeekView{Gameweek: gw, Value: round1(total), Double: doubles > 0,
			Detail: fmt.Sprintf("%d of your 4 bench players have a match%s", playing, map[bool]string{true: fmt.Sprintf(", %d of them two", doubles), false: ""}[doubles > 0])})
	}
	verdict, best, now := verdictFor(a.Weeks, base.next, 0)
	a.Verdict, a.BestGameweek, a.BestValue, a.ThisWeek = verdict, best.Gameweek, best.Value, now
	switch verdict {
	case "good_week_now":
		a.Headline = fmt.Sprintf("This week is as good as any for it: your bench is expected to score %.1f in GW%d, the best of the next %d (or within a toss-up).", now, base.next, len(a.Weeks))
	case "wait":
		a.Headline = fmt.Sprintf("Wait for GW%d: your bench is expected to score %.1f then (%s), against %.1f this week.", best.Gameweek, best.Value, best.Detail, now)
	default:
		a.Headline = "Your bench is not expected to score in any of the next gameweeks."
	}
	for i, w := range weeklyBest(a.Weeks) {
		if i == 3 {
			break
		}
		a.Insights = append(a.Insights, fmt.Sprintf("GW%d: bench expected to score %.1f (%s).", w.Gameweek, w.Value, w.Detail))
	}
	a.Insights = append(a.Insights, "The bench is whoever your best XI leaves out that week, from your current squad. Strengthening the bench before the chip raises these figures.")
	return a, nil
}

// ---- Free Hit: how much the best possible squad for one week beats yours by.

func freeHitAdvice(base *baseSnap, sq *squadSnap, last int) (ChipAdviceView, error) {
	a := ChipAdviceView{Chip: chipFreeHit, Insights: []string{}, Weeks: []ChipWeekView{}}
	squad := sq.team.Squad.Players
	funds := sq.fin.Bank
	for _, p := range squad {
		funds += sq.fin.Sell[p.ID]
	}
	for gw := base.next; gw <= last; gw++ {
		xp := base.model.ScoreFrom(gw, 1)
		cur, err := lineup.Advise(squad, lineup.Options{XP: xp})
		if err != nil {
			return a, err
		}
		var cands []optimizer.Candidate
		for _, p := range base.players {
			if optimizer.Available(p) {
				cands = append(cands, optimizer.Candidate{Player: p, Cost: p.NowCost, Score: xp(p)})
			}
		}
		best, err := optimizer.BestSquad(cands, funds)
		if err != nil {
			return a, fmt.Errorf("free hit squad for GW%d: %w", gw, err)
		}
		fh, err := lineup.Advise(best.Squad.Players, lineup.Options{XP: xp})
		if err != nil {
			return a, err
		}
		blank := 0
		for _, p := range squad {
			if len(fixturesIn(base, p, gw)) == 0 {
				blank++
			}
		}
		gain := round1(fh.Total) - round1(cur.Total)
		gain = round1(gain)
		detail := fmt.Sprintf("your XI %.1f, a Free Hit XI %.1f", round1(cur.Total), round1(fh.Total))
		if blank > 0 {
			detail += fmt.Sprintf("; %d of your players have no match", blank)
		}
		a.Weeks = append(a.Weeks, ChipWeekView{Gameweek: gw, Value: gain, Detail: detail, Double: false})
	}
	verdict, best, now := verdictFor(a.Weeks, base.next, tossUp)
	a.Verdict, a.BestGameweek, a.BestValue, a.ThisWeek = verdict, best.Gameweek, best.Value, now
	switch verdict {
	case "good_week_now":
		a.Headline = fmt.Sprintf("This week is among the best for it: a Free Hit squad would beat yours by %.1f in GW%d.", now, base.next)
	case "wait":
		a.Headline = fmt.Sprintf("Wait for GW%d: a Free Hit squad would beat yours by %.1f then (%s), against %.1f this week.", best.Gameweek, best.Value, best.Detail, now)
	default:
		a.Headline = fmt.Sprintf("No gameweek in the next stretch stands out: the best a Free Hit squad manages is %.1f better than yours.", best.Value)
	}
	for i, w := range weeklyBest(a.Weeks) {
		if i == 3 {
			break
		}
		a.Insights = append(a.Insights, fmt.Sprintf("GW%d: %+.1f (%s).", w.Gameweek, w.Value, w.Detail))
	}
	a.Insights = append(a.Insights, "A Free Hit is worth most in a gameweek when many of your players have no match: it picks a fresh squad for that week only and your own squad comes back after.")
	return a, nil
}

// ---- Wildcard: the gain from rebuilding the squad, counted over the next few gameweeks.

func wildcardAdvice(base *baseSnap, sq *squadSnap, last int) (ChipAdviceView, error) {
	a := ChipAdviceView{Chip: chipWildcard, Insights: []string{}, Weeks: []ChipWeekView{}}
	squad := sq.team.Squad.Players
	in := transfers.Input{Squad: squad, Sell: sq.fin.Sell, Bank: sq.fin.Bank, Pool: base.players}
	type tryResult struct {
		gw      int
		net     float64
		changed int
	}
	var tries []tryResult
	for gw := base.next; gw < base.next+wildcardStarts && gw+wildcardWindow-1 <= lastGameweek; gw++ {
		sug, err := transfers.Wildcard(in, transfers.Options{Score: base.model.ScoreFrom(gw, wildcardWindow), Horizon: wildcardWindow, FreeTransfers: 0})
		if err != nil {
			return a, err
		}
		tries = append(tries, tryResult{gw, round1(sug.Net), sug.Transfers})
		a.Weeks = append(a.Weeks, ChipWeekView{Gameweek: gw, Value: round1(sug.Net),
			Detail: fmt.Sprintf("changes %d %s, counted over GW%d-GW%d", sug.Transfers, plural(sug.Transfers, "player", "players"), gw, gw+wildcardWindow-1)})
	}
	verdict, best, now := verdictFor(a.Weeks, base.next, tossUp)
	a.Verdict, a.BestGameweek, a.BestValue, a.ThisWeek = verdict, best.Gameweek, best.Value, now
	switch verdict {
	case "good_week_now":
		a.Headline = fmt.Sprintf("Playing it now would add %.1f points over the next %d gameweeks (%s).", now, wildcardWindow, a.Weeks[0].Detail)
	case "wait":
		a.Headline = fmt.Sprintf("Playing it in GW%d would add more: %.1f over %d gameweeks, against %.1f if played now.", best.Gameweek, best.Value, wildcardWindow, now)
	default:
		a.Headline = fmt.Sprintf("A Wildcard would not add much yet: the best it manages in the next few weeks is %.1f over %d gameweeks.", best.Value, wildcardWindow)
	}
	for _, t := range tries {
		a.Insights = append(a.Insights, fmt.Sprintf("From GW%d: %+.1f over GW%d-GW%d, changing %d %s.", t.gw, t.net, t.gw, t.gw+wildcardWindow-1, t.changed, plural(t.changed, "player", "players")))
	}
	a.Insights = append(a.Insights, "These assume your squad stays as it is until the chip is played, and no transfer hit is charged, since a Wildcard has none.")
	return a, nil
}
