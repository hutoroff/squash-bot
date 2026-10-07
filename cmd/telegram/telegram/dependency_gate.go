package telegram

import (
	"context"
	"sync/atomic"
	"time"
)

// DependencyGate records whether a required runtime dependency is currently
// reachable. It is deliberately only an admission gate: it neither queues
// updates nor retries management calls.
type DependencyGate struct {
	available atomic.Bool
}

func NewDependencyGate(available bool) *DependencyGate {
	gate := &DependencyGate{}
	gate.available.Store(available)
	return gate
}

func (g *DependencyGate) Available() bool {
	return g != nil && g.available.Load()
}

func (g *DependencyGate) SetAvailable(available bool) {
	if g != nil {
		g.available.Store(available)
	}
}

// Observe refreshes availability immediately and at interval until ctx is
// cancelled. check must bound its own dependency request.
func (g *DependencyGate) Observe(ctx context.Context, interval time.Duration, check func(context.Context) error) {
	if g == nil || interval <= 0 || check == nil {
		return
	}
	refresh := func() { g.SetAvailable(check(ctx) == nil) }
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
