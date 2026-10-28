package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

type usageStore struct{ n map[string]int }

func (u *usageStore) TakeUsage(_ context.Context, user, kind string, day time.Time, limit int) (int, bool, error) {
	k := user + "|" + kind + "|" + day.Format(time.DateOnly)
	if u.n[k] >= limit {
		return limit, false, nil
	}
	u.n[k]++
	return u.n[k], true, nil
}

func (u *usageStore) UsageOn(_ context.Context, user string, day time.Time) (map[string]int, error) {
	out := map[string]int{}
	for k, n := range u.n {
		parts := strings.Split(k, "|")
		if parts[0] == user && parts[2] == day.Format(time.DateOnly) {
			out[parts[1]] = n
		}
	}
	return out, nil
}

func limitsRig() *service.Limits {
	return &service.Limits{
		Store:  &usageStore{n: map[string]int{}},
		Owners: service.ParseOwners(" Owner@Example.com , kiran@example.com,"),
		Daily:  map[string]int{service.LimitAsk: 2, service.LimitSearch: 0, service.LimitRefresh: 1},
		Now:    func() time.Time { return time.Date(2026, 10, 28, 21, 30, 0, 0, time.UTC) },
	}
}

func TestLimits_TakeStopsAVisitorAtTheDailyLimit(t *testing.T) {
	l, ctx := limitsRig(), context.Background()

	for i := 0; i < 2; i++ {
		if err := l.Take(ctx, "Visitor@example.com", service.LimitAsk); err != nil {
			t.Fatalf("question %d: %v", i+1, err)
		}
	}
	err := l.Take(ctx, "visitor@example.com", service.LimitAsk) // the same user, whatever the case
	var le *service.LimitError
	if !errors.As(err, &le) || !errors.Is(err, service.ErrLimitReached) || !strings.Contains(err.Error(), "2 ask questions") {
		t.Fatalf("third question: %v", err)
	}
	if err := l.Take(ctx, "visitor@example.com", service.LimitRefresh); err != nil {
		t.Errorf("another feature has its own budget: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := l.Take(ctx, "visitor@example.com", service.LimitSearch); err != nil {
			t.Errorf("a limit of 0 is unlimited: %v", err)
		}
	}
}

func TestLimits_OwnersAndTheLocalUserAreUnlimited(t *testing.T) {
	l, ctx := limitsRig(), context.Background()
	for _, user := range []string{"owner@example.com", "OWNER@example.com", "kiran@example.com", "local"} {
		for i := 0; i < 5; i++ {
			if err := l.Take(ctx, user, service.LimitAsk); err != nil {
				t.Fatalf("%s: %v", user, err)
			}
		}
	}
	var none *service.Limits
	if err := none.Take(ctx, "anyone", service.LimitAsk); err != nil {
		t.Errorf("no limits configured: %v", err)
	}
}

func TestLimits_Usage(t *testing.T) {
	l, ctx := limitsRig(), context.Background()
	_ = l.Take(ctx, "visitor@example.com", service.LimitAsk)

	v, err := l.Usage(ctx, "visitor@example.com")
	if err != nil || v.Unlimited || len(v.Items) != 3 || v.Items[0].Used != 1 || v.Items[0].Limit != 2 || v.ResetsIn != "2h" {
		t.Errorf("%+v %v", v, err)
	}
	owner, _ := l.Usage(ctx, "owner@example.com")
	if !owner.Unlimited || len(owner.Items) != 0 {
		t.Errorf("owner: %+v", owner)
	}
}

type pruner struct {
	mu     sync.Mutex
	before []time.Time
}

func (p *pruner) PruneUsage(_ context.Context, before time.Time) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.before = append(p.before, before)
	return 0, nil
}

func TestPruneDailyKeepsThirtyDaysAndStopsWithItsContext(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	p := &pruner{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.PruneDaily(ctx, p, func() time.Time { return now }, 10*time.Millisecond)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for {
		p.mu.Lock()
		n := len(p.before)
		p.mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("pruned fewer than twice")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PruneDaily did not stop when its context ended")
	}
	if want := now.Add(-30 * 24 * time.Hour); !p.before[0].Equal(want) {
		t.Errorf("pruned before %v, want %v", p.before[0], want)
	}
}

func TestIsOwner(t *testing.T) {
	l := limitsRig()
	for user, want := range map[string]bool{"owner@example.com": true, "OWNER@example.com": true, "local": true, "visitor@example.com": false, "": false} {
		if got := l.IsOwner(user); got != want {
			t.Errorf("%q: %v, want %v", user, got, want)
		}
	}
}
