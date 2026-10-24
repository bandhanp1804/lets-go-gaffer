package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/bandhanpatel/lets-go-gaffer/internal/notify"
)

// Signals a watch workflow takes.
const (
	SignalNews   = "news"
	SignalStatus = "status"
)

// NewsSignal is a new story about the watched player.
type NewsSignal struct {
	Title  string
	URL    string
	Source string
}

// StatusSignal is the watched player's FPL status and note after a change.
type StatusSignal struct {
	Name   string
	Status string
	News   string
}

// WatchInput is a watch's state. It is carried across continue-as-new, so the last status
// is remembered and an unchanged one is never announced twice.
type WatchInput struct {
	PlayerID   int
	Name       string
	LastStatus string
	LastNews   string
	Events     int
}

// watchEventsPerRun bounds one run's history; the watch then continues as a new run.
const watchEventsPerRun = 200

// WatchPlayer turns news and status signals about one player into alerts, for as long as he is
// watched. Unwatching cancels it, and it ends quietly.
func WatchPlayer(ctx workflow.Context, in WatchInput) error {
	var a *Activities
	notifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})
	newsCh := workflow.GetSignalChannel(ctx, SignalNews)
	statusCh := workflow.GetSignalChannel(ctx, SignalStatus)
	done := false

	sel := workflow.NewSelector(ctx)
	sel.AddReceive(newsCh, func(c workflow.ReceiveChannel, _ bool) {
		var s NewsSignal
		c.Receive(ctx, &s)
		in.Events++
		alert(ctx, notifyCtx, a, newsMessage(in, s))
	})
	sel.AddReceive(statusCh, func(c workflow.ReceiveChannel, _ bool) {
		var s StatusSignal
		c.Receive(ctx, &s)
		in.Events++
		if s.Name != "" {
			in.Name = s.Name
		}
		if s.Status == in.LastStatus && s.News == in.LastNews {
			return
		}
		in.LastStatus, in.LastNews = s.Status, s.News
		alert(ctx, notifyCtx, a, statusMessage(in, s))
	})
	sel.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { done = true })

	for !done {
		sel.Select(ctx)
		if in.Events >= watchEventsPerRun {
			// Signals that arrived meanwhile belong to this run: handle them before moving on.
			for sel.HasPending() && !done {
				sel.Select(ctx)
			}
			if done {
				break
			}
			in.Events = 0
			return workflow.NewContinueAsNewError(ctx, WatchPlayer, in)
		}
	}
	return nil
}

func alert(ctx, notifyCtx workflow.Context, a *Activities, m notify.Message) {
	if err := workflow.ExecuteActivity(notifyCtx, a.Notify, m).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("alert not delivered", "title", m.Title, "err", err)
	}
}

func newsMessage(in WatchInput, s NewsSignal) notify.Message {
	return notify.Message{Kind: "watch", PlayerID: in.PlayerID, URL: s.URL,
		Title: fmt.Sprintf("%s in the news", in.Name), Body: fmt.Sprintf("%s (%s)", s.Title, s.Source)}
}

func statusMessage(in WatchInput, s StatusSignal) notify.Message {
	body := s.News
	if body == "" {
		body = "FPL has cleared his note."
	}
	return notify.Message{Kind: "watch", PlayerID: in.PlayerID, Title: fmt.Sprintf("%s: %s", in.Name, s.Status), Body: body}
}
