package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

const otherTeam = 777

func TestViewer_ANamedTeamGetsItsOwnPublicSquad(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam})

	got, err := r.svc.MyTeam(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "public" {
		t.Errorf("source = %q, want public", got.Source)
	}
}

// The configured token is the owner's login. It must never ride along on someone else's team.
func TestViewer_TheOwnersConfiguredTokenIsNeverUsedForAnotherTeam(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live, sessionFn: func() string { return token(testNow.Add(time.Hour)) }})

	if _, err := r.svc.MyTeam(service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam})); err != nil {
		t.Fatal(err)
	}
	if live.calls != 0 {
		t.Fatalf("the owner's token was sent to FPL for team %v", live.teams)
	}

	own, err := r.svc.MyTeam(context.Background()) // no viewer: the configured team, with its token
	if err != nil {
		t.Fatal(err)
	}
	if own.Source != "live" || !reflect.DeepEqual(live.teams, []int{teamID}) {
		t.Errorf("the owner's own squad should be live: source %q calls for %v", own.Source, live.teams)
	}
}

func TestViewer_ARequestsOwnTokenGivesALiveSquadForItsTeam(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live})
	ctx := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam, Token: token(testNow.Add(30 * time.Minute))})

	got, err := r.svc.MyTeam(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "live" || !reflect.DeepEqual(live.teams, []int{otherTeam}) {
		t.Errorf("source %q, live calls for %v", got.Source, live.teams)
	}
}

func TestViewer_DifferentTeamsAndTokensAreCachedSeparately(t *testing.T) {
	live := &fakeLive{team: liveTeam(9, 1, 2, 4)}
	r := newRig(t, rigOpts{live: live})
	ctx := context.Background()
	public := service.WithViewer(ctx, service.Viewer{TeamID: otherTeam})
	withToken := service.WithViewer(ctx, service.Viewer{TeamID: otherTeam, Token: token(testNow.Add(time.Hour))})

	a, _ := r.svc.MyTeam(public)
	b, _ := r.svc.MyTeam(withToken) // the same team, now logged in: must not be served the public squad
	c, _ := r.svc.MyTeam(public)    // and logging in must not turn the public view live

	if a.Source != "public" || b.Source != "live" || c.Source != "public" {
		t.Errorf("sources %q %q %q, want public live public", a.Source, b.Source, c.Source)
	}
	if live.calls != 1 {
		t.Errorf("live FPL calls = %d, want 1 (the live squad is cached too)", live.calls)
	}
}

func TestViewer_NoTeamAnywhereIsATypedError(t *testing.T) {
	r := newRig(t, rigOpts{})
	svc := service.New(service.Deps{Store: r.store, FPL: r.fpl, Now: func() time.Time { return testNow }}, service.Config{}) // no configured team

	_, err := svc.MyTeam(context.Background())
	if !errors.Is(err, service.ErrTeamRequired) {
		t.Errorf("err = %v, want ErrTeamRequired", err)
	}
	if _, err := svc.MyTeam(service.WithViewer(context.Background(), service.Viewer{TeamID: teamID})); err != nil {
		t.Errorf("a named team needs no configured one: %v", err)
	}
}

func TestViewer_AnUnknownTeamIsAnInputErrorWithAHelpfulMessage(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.fpl.missing = map[int]bool{otherTeam: true}

	_, err := r.svc.MyTeam(service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam}))

	var in *service.InputError
	if !errors.As(err, &in) {
		t.Fatalf("err = %v, want an InputError (a 400, not a 500)", err)
	}
}

func TestViewer_ARejectedTokenFallsBackToPublicWithAnExplanation(t *testing.T) {
	live := &fakeLive{err: wrapRejected()}
	r := newRig(t, rigOpts{live: live})
	ctx := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam, Token: token(testNow.Add(time.Hour))})

	got, err := r.svc.MyTeam(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "public" || !hasNote(got.Notes, "belongs to a different team") {
		t.Errorf("source %q notes %v", got.Source, got.Notes)
	}
}

func TestViewer_StatusReportsTheViewersOwnSession(t *testing.T) {
	r := newRig(t, rigOpts{live: &fakeLive{}})
	mine := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam, Token: token(testNow.Add(20 * time.Minute))})
	theirs := service.WithViewer(context.Background(), service.Viewer{TeamID: otherTeam})

	a, _ := r.svc.Status(mine)
	b, _ := r.svc.Status(theirs)

	if a.Session.State != "valid" || b.Session.State != "not configured" {
		t.Errorf("with token %+v, without %+v", a.Session, b.Session)
	}
}
