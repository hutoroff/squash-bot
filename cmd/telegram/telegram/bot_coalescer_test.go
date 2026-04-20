package telegram

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGameEditWorker_SingleCall verifies that a lone schedule() call runs
// the function exactly once.
func TestGameEditWorker_SingleCall(t *testing.T) {
	var calls atomic.Int64
	w := &gameEditWorker{}

	done := make(chan struct{})
	w.schedule(func() {
		calls.Add(1)
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not complete within timeout")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("run called %d times, want 1", n)
	}
}

// TestGameEditWorker_ConcurrentCalls is the core correctness test.
// N goroutines call schedule() concurrently while the first run is blocked.
// The run function must be called at most twice — the initial run plus at
// most one coalesced follow-up — regardless of N.
func TestGameEditWorker_ConcurrentCalls(t *testing.T) {
	const goroutines = 50

	var calls atomic.Int64
	unblock := make(chan struct{})
	allDone := make(chan struct{})

	w := &gameEditWorker{}

	// First schedule: the run blocks until we release it, giving all other
	// goroutines time to call schedule() and set pending.
	w.schedule(func() {
		calls.Add(1)
		if calls.Load() == 1 {
			<-unblock
		}
		// Second run (if pending) returns immediately.
	})

	// Spawn N-1 more callers while the first run is blocked.
	var wg sync.WaitGroup
	for i := 0; i < goroutines-1; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.schedule(func() { calls.Add(1) })
		}()
	}
	// Give all goroutines a moment to call schedule().
	time.Sleep(10 * time.Millisecond)

	// Unblock the first run; the worker will loop once more if pending.
	close(unblock)

	// Wait for the worker to go idle.
	go func() {
		wg.Wait()
		// Poll until the worker is idle (running == false).
		for {
			w.mu.Lock()
			idle := !w.running
			w.mu.Unlock()
			if idle {
				close(allDone)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	select {
	case <-allDone:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not go idle within timeout")
	}

	n := calls.Load()
	if n < 1 || n > 2 {
		t.Errorf("run called %d times for %d concurrent callers, want 1 or 2", n, goroutines)
	}
}

// TestGameEditWorker_IdleAfterCompletion verifies that the worker is reusable:
// a second schedule() call after the first run completes starts a fresh goroutine.
func TestGameEditWorker_IdleAfterCompletion(t *testing.T) {
	var calls atomic.Int64
	w := &gameEditWorker{}

	run := func() {
		done := make(chan struct{})
		w.schedule(func() {
			calls.Add(1)
			close(done)
		})
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("worker did not complete")
		}
	}

	run()
	run()

	if n := calls.Load(); n != 2 {
		t.Errorf("run called %d times across two sequential schedules, want 2", n)
	}
}
