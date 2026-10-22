// Package fpltest provides test doubles for fpl.FPLClient.
package fpltest

import (
	"context"
	"fmt"
	"sync"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
)

// Fake is an in-memory fpl.FPLClient that counts calls. Safe for concurrent use.
type Fake struct {
	mu             sync.Mutex
	bootstrap      *fpl.Bootstrap
	histories      map[int][]fpl.PlayerHistory
	err            error
	bootstrapCalls int
	historyCalls   map[int]int
}

func NewFake(b *fpl.Bootstrap, histories map[int][]fpl.PlayerHistory) *Fake {
	return &Fake{bootstrap: b, histories: histories, historyCalls: map[int]int{}}
}

// SetErr makes every subsequent call fail with err; pass nil to recover.
func (f *Fake) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *Fake) BootstrapCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bootstrapCalls
}

func (f *Fake) HistoryCalls(playerID int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.historyCalls[playerID]
}

func (f *Fake) FetchBootstrap(ctx context.Context) (*fpl.Bootstrap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootstrapCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.bootstrap, nil
}

func (f *Fake) FetchPlayerHistory(ctx context.Context, playerID int) ([]fpl.PlayerHistory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls[playerID]++
	if f.err != nil {
		return nil, f.err
	}
	h, ok := f.histories[playerID]
	if !ok {
		return nil, fmt.Errorf("fake: no history for player %d", playerID)
	}
	return h, nil
}
