package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

// briefingLead is how long before the deadline the briefing goes out.
const briefingLead = 24 * time.Hour

// Deadline is the next gameweek and when its deadline is.
type Deadline struct {
	Gameweek int
	At       time.Time
}

// BriefingCheckInput carries the feed list on to the briefing's news pull.
type BriefingCheckInput struct {
	Feeds []string
}

// BriefingInput is one gameweek's briefing.
type BriefingInput struct {
	Gameweek int
	Deadline time.Time
	Feeds    []string
}

// BriefingID is the workflow id of a gameweek's briefing: one per gameweek, ever.
func BriefingID(gw int) string { return fmt.Sprintf("briefing-gw%d", gw) }

// BriefingCheck runs every hour. It makes sure the next gameweek has a briefing workflow
// waiting for it, and does nothing if one already exists (running, or finished).
func BriefingCheck(ctx workflow.Context, in BriefingCheckInput) error {
	var a *Activities
	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var next Deadline
	if err := workflow.ExecuteActivity(quick, a.NextDeadline).Get(ctx, &next); err != nil {
		return err
	}
	if next.Gameweek == 0 || !next.At.After(workflow.Now(ctx)) {
		return nil // the season is over, or the deadline has passed
	}
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: BriefingID(next.Gameweek),
		// The briefing outlives this hourly check, and a gameweek's briefing never runs twice.
		ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	})
	f := workflow.ExecuteChildWorkflow(child, GameweekBriefing, BriefingInput{Gameweek: next.Gameweek, Deadline: next.At, Feeds: in.Feeds})
	err := f.GetChildWorkflowExecution().Get(ctx, nil)
	var started *temporal.ChildWorkflowExecutionAlreadyStartedError
	if errors.As(err, &started) {
		return nil
	}
	return err
}

// GameweekBriefing sleeps until a day before the deadline, pulls the latest news, and sends the
// manager the plan for the gameweek. The sleep is a durable timer: a restarted worker carries on
// waiting, and a worker that was down at the time sends it as soon as it is back, unless the
// deadline has passed by then.
func GameweekBriefing(ctx workflow.Context, in BriefingInput) error {
	var a *Activities
	if wait := in.Deadline.Add(-briefingLead).Sub(workflow.Now(ctx)); wait > 0 {
		if err := workflow.Sleep(ctx, wait); err != nil {
			return err
		}
	}
	if !workflow.Now(ctx).Before(in.Deadline) {
		workflow.GetLogger(ctx).Info("deadline passed before the briefing could go out", "gameweek", in.Gameweek)
		return nil
	}

	newsCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: fmt.Sprintf("news-refresh-briefing-gw%d", in.Gameweek)})
	if err := workflow.ExecuteChildWorkflow(newsCtx, NewsRefresh, NewsInput{Feeds: in.Feeds}).Get(ctx, nil); err != nil {
		// Stale news is better than no briefing.
		workflow.GetLogger(ctx).Warn("news pull before the briefing failed", "err", err)
	}

	send := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 4},
	})
	return workflow.ExecuteActivity(send, a.SendBriefing, in.Gameweek, in.Deadline).Get(ctx, nil)
}

// NextDeadline reads the upcoming gameweek and its deadline.
func (a *Activities) NextDeadline(ctx context.Context) (Deadline, error) {
	st, err := a.Advisor.Status(ctx)
	if err != nil {
		return Deadline{}, err
	}
	if st.Deadline == "" {
		return Deadline{}, nil
	}
	at, err := time.Parse(time.RFC3339, st.Deadline)
	if err != nil {
		return Deadline{}, fmt.Errorf("read deadline %q: %w", st.Deadline, err)
	}
	return Deadline{Gameweek: st.Gameweek, At: at}, nil
}

// SendBriefing builds the briefing for the configured team and sends it, once per gameweek.
// The gameweek is claimed first; if sending then fails the claim is released, so the retry sends it.
func (a *Activities) SendBriefing(ctx context.Context, gw int, deadline time.Time) error {
	first, err := a.Store.MarkBriefingSent(ctx, gw)
	if err != nil {
		return err
	}
	if !first {
		return nil
	}
	m, err := a.briefing(ctx, gw, deadline)
	if err == nil {
		err = a.Notifier.Notify(ctx, m)
	}
	if err != nil {
		if rerr := a.Store.ReleaseBriefing(ctx, gw); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	return nil
}

func (a *Activities) briefing(ctx context.Context, gw int, deadline time.Time) (notify.Message, error) {
	l, err := a.Advisor.LineupWith(ctx, service.LineupRequest{})
	if errors.Is(err, service.ErrTeamRequired) {
		return notify.Message{}, permanent("no team is configured: set GAFFER_TEAM_ID in .env for the briefing")
	}
	if err != nil {
		return notify.Message{}, err
	}
	t, err := a.Advisor.Transfers(ctx, service.TransfersRequest{})
	if err != nil {
		return notify.Message{}, err
	}
	c, err := a.Advisor.ChipsFor(ctx, service.ChipsRequest{})
	if err != nil {
		return notify.Message{}, err
	}
	return Briefing(gw, deadline, l, t, c), nil
}

// Briefing writes the gameweek plan as a message. Every number comes from the views, which
// computed it in Go; this only arranges them.
func Briefing(gw int, deadline time.Time, l service.LineupView, t service.TransfersView, c service.ChipsView) notify.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "Deadline %s.\n", deadline.Local().Format("Mon 2 Jan 15:04"))
	fmt.Fprintf(&b, "Captain %s, vice %s.\n", l.Captain, l.ViceCaptain)
	fmt.Fprintf(&b, "Best XI: %s, %.1f expected points.\n", l.Formation, l.ExpectedPoints)

	var flags []string
	for _, p := range append(append([]service.PlayerView{}, l.Starting...), l.Bench...) {
		if !strings.HasPrefix(p.Status, "available") {
			flags = append(flags, fmt.Sprintf("%s %s", p.Name, p.Status))
		}
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, "Check: %s.\n", strings.Join(flags, ", "))
	}

	if len(t.Suggestions) == 0 {
		b.WriteString("Transfers: keep the squad as it is.\n")
	} else {
		s := t.Suggestions[0]
		var moves []string
		for _, m := range s.Moves {
			moves = append(moves, fmt.Sprintf("%s → %s", m.Out.Name, m.In.Name))
		}
		hit := ""
		if s.Hit > 0 {
			hit = fmt.Sprintf(", a %d-point hit", s.Hit)
		}
		fmt.Fprintf(&b, "Transfers: %s (%+.1f net over %d gameweeks%s).\n", strings.Join(moves, ", "), s.NetPoints, t.Horizon, hit)
	}
	for _, chip := range c.Chips {
		if chip.Available && chip.Verdict == "good_week_now" {
			fmt.Fprintf(&b, "Chip: %s. %s\n", chip.Name, chip.Headline)
		}
	}
	return notify.Message{Kind: "briefing", Title: fmt.Sprintf("GW%d briefing: captain %s", gw, l.Captain), Body: strings.TrimSpace(b.String())}
}
