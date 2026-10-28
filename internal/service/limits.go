package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The features with a daily limit: each spends an API credit (Claude or Voyage) per use.
const (
	LimitAsk     = "ask"
	LimitSearch  = "search"
	LimitRefresh = "refresh"
)

var limitLabels = map[string]string{
	LimitAsk:     "Ask questions",
	LimitSearch:  "News searches",
	LimitRefresh: "News refreshes",
}

// LimitError says a user has used up a feature for today.
type LimitError struct {
	Kind  string
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("you have used today's %d %s; it resets at midnight UTC", e.Limit, strings.ToLower(limitLabels[e.Kind]))
}

// ErrLimitReached matches any LimitError.
var ErrLimitReached = errors.New("daily limit reached")

func (e *LimitError) Is(target error) bool { return target == ErrLimitReached }

// UsageStore counts uses per user, kind and day. *db.Store satisfies it.
type UsageStore interface {
	TakeUsage(ctx context.Context, user, kind string, day time.Time, limit int) (int, bool, error)
	UsageOn(ctx context.Context, user string, day time.Time) (map[string]int, error)
}

// Limits gives each signed-in user a daily budget of the features that spend API credits, so an
// open sign-in cannot run up the owner's bill. Owners, and the local user on the owner's own
// computer, are never limited. A limit of 0 turns that feature's limit off.
type Limits struct {
	Store  UsageStore
	Owners map[string]bool // lower-case emails or usernames
	Daily  map[string]int  // kind -> uses per day
	Now    func() time.Time
}

// UsageItemView is one limited feature for one user today.
type UsageItemView struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Used  int    `json:"used"`
	Limit int    `json:"limit"` // 0: unlimited
}

// UsageView is a user's limits and use today.
type UsageView struct {
	Unlimited bool            `json:"unlimited"` // an owner, or the local user
	Items     []UsageItemView `json:"items"`
	ResetsIn  string          `json:"resets_in"` // until midnight UTC
}

func (l *Limits) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// exempt says whether a user has no limits.
func (l *Limits) exempt(user string) bool {
	return l == nil || user == "local" || l.Owners[strings.ToLower(user)]
}

// Take counts one use of kind by user, or returns a *LimitError if today's are used up.
func (l *Limits) Take(ctx context.Context, user, kind string) error {
	if l.exempt(user) {
		return nil
	}
	limit := l.Daily[kind]
	if limit <= 0 {
		return nil
	}
	_, ok, err := l.Store.TakeUsage(ctx, strings.ToLower(user), kind, l.now(), limit)
	if err != nil {
		return err
	}
	if !ok {
		return &LimitError{Kind: kind, Limit: limit}
	}
	return nil
}

// Usage reports a user's limits and use today.
func (l *Limits) Usage(ctx context.Context, user string) (UsageView, error) {
	now := l.now()
	midnight := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	v := UsageView{Items: []UsageItemView{}, ResetsIn: ago(midnight.Sub(now))}
	if l.exempt(user) {
		v.Unlimited = true
		return v, nil
	}
	used, err := l.Store.UsageOn(ctx, strings.ToLower(user), now)
	if err != nil {
		return UsageView{}, err
	}
	for _, kind := range []string{LimitAsk, LimitSearch, LimitRefresh} {
		v.Items = append(v.Items, UsageItemView{Kind: kind, Label: limitLabels[kind], Used: used[kind], Limit: l.Daily[kind]})
	}
	return v, nil
}

// ParseOwners reads a comma-separated list of emails or usernames.
func ParseOwners(s string) map[string]bool {
	out := map[string]bool{}
	for _, o := range strings.Split(s, ",") {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			out[o] = true
		}
	}
	return out
}
