package telegram

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDependencyGateObserveTracksOutageAndRecovery(t *testing.T) {
	gate := NewDependencyGate(true)
	var unavailable atomic.Bool
	unavailable.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go gate.Observe(ctx, time.Millisecond, func(context.Context) error {
		if unavailable.Load() {
			return errors.New("management unavailable")
		}
		return nil
	})
	waitForGate(t, gate, false)

	unavailable.Store(false)
	waitForGate(t, gate, true)
}

func TestDependencyGateNilIsUnavailable(t *testing.T) {
	var gate *DependencyGate
	if gate.Available() {
		t.Fatal("nil dependency gate must not admit updates")
	}
}

func waitForGate(t *testing.T, gate *DependencyGate, expected bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if gate.Available() == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gate availability = %t, want %t", gate.Available(), expected)
}
