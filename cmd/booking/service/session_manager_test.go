package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

type testSession struct {
	id     int
	closed atomic.Bool
}

func (s *testSession) Close() error {
	s.closed.Store(true)
	return nil
}

func TestSessionLeaseDiscardClosesSuspectSessionAndKeepsAccountGateUntilRelease(t *testing.T) {
	manager := newTestSessionManager(t, 2, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{id: 1}, nil
	})
	lease, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "player@example.com", Password: "one"})
	if err != nil {
		t.Fatal(err)
	}
	session := lease.Session.(*testSession)
	lease.Discard()
	if !session.closed.Load() || manager.Len() != 0 {
		t.Fatalf("discard left session open/in cache: closed=%t len=%d", session.closed.Load(), manager.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := manager.Acquire(ctx, "account-1", bookingapi.Credentials{Email: "player@example.com", Password: "one"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("discard released account gate early: %v", err)
	}
	lease.Release()
	replacement, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "player@example.com", Password: "one"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.Release()
}

func TestSessionManagerSerializesAccountAndConstructsAtomically(t *testing.T) {
	var creations atomic.Int32
	manager := newTestSessionManager(t, 4, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{id: int(creations.Add(1))}, nil
	})
	credentials := bookingapi.Credentials{Email: "player@example.com", Password: "one"}
	first, err := manager.Acquire(context.Background(), "account-1", credentials)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan *SessionLease, 1)
	errs := make(chan error, 1)
	go func() {
		lease, err := manager.Acquire(context.Background(), "account-1", credentials)
		if err != nil {
			errs <- err
			return
		}
		acquired <- lease
	}()
	select {
	case <-acquired:
		t.Fatal("second lease acquired while the account was active")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(30 * time.Millisecond):
	}
	first.Release()

	var second *SessionLease
	select {
	case second = <-acquired:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second lease did not acquire after release")
	}
	defer second.Release()
	if second.Session != first.Session {
		t.Fatal("same credential version did not reuse the session")
	}
	if creations.Load() != 1 {
		t.Fatalf("session creations = %d, want 1", creations.Load())
	}
}

func TestSessionManagerPasswordRotationKeepsAccountDomainAndEvictsOldSession(t *testing.T) {
	var mu sync.Mutex
	var created []*testSession
	manager := newTestSessionManager(t, 4, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		mu.Lock()
		defer mu.Unlock()
		session := &testSession{id: len(created) + 1}
		created = append(created, session)
		return session, nil
	})
	first, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "Player@Example.com", Password: "old"})
	if err != nil {
		t.Fatal(err)
	}
	firstSession := first.Session.(*testSession)
	first.Release()

	second, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "player@example.com", Password: "new"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if second.Session == first.Session {
		t.Fatal("password rotation reused the old session")
	}
	if !firstSession.closed.Load() {
		t.Fatal("obsolete idle session was not closed")
	}
	if manager.Len() != 1 {
		t.Fatalf("cache length = %d, want 1", manager.Len())
	}
}

func TestSessionManagerWaitIsContextAware(t *testing.T) {
	manager := newTestSessionManager(t, 2, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{}, nil
	})
	first, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "a", Password: "one"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = manager.Acquire(ctx, "account-1", bookingapi.Credentials{Email: "a", Password: "two"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire error = %v, want deadline exceeded", err)
	}
}

func TestSessionManagerEvictsIdleLRUAndNeverPinnedEntry(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	var creations atomic.Int32
	manager, err := NewSessionManager(SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{id: int(creations.Add(1))}, nil
	}), SessionManagerConfig{
		MaxEntries: 2, IdleTTL: time.Hour, Clock: func() time.Time { return now }, CacheKey: []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "one", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	now = now.Add(time.Minute)
	pinned, err := manager.Acquire(context.Background(), "account-2", bookingapi.Credentials{Email: "two", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	firstSession := first.Session.(*testSession)
	now = now.Add(time.Minute)
	third, err := manager.Acquire(context.Background(), "account-3", bookingapi.Credentials{Email: "three", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer third.Release()
	defer pinned.Release()
	if !firstSession.closed.Load() {
		t.Fatal("oldest idle session was not evicted")
	}
	if pinned.Session.(*testSession).closed.Load() {
		t.Fatal("pinned session was evicted")
	}
}

func TestSessionManagerExpiresIdleSession(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	manager, err := NewSessionManager(SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{}, nil
	}), SessionManagerConfig{
		MaxEntries: 2, IdleTTL: time.Minute, Clock: func() time.Time { return now }, CacheKey: []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "one", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	firstSession := first.Session.(*testSession)
	first.Release()
	now = now.Add(time.Minute)
	second, err := manager.Acquire(context.Background(), "account-2", bookingapi.Credentials{Email: "two", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	if !firstSession.closed.Load() {
		t.Fatal("expired idle session was not closed")
	}
}

func TestSessionManagerClosesPinnedSessionAfterShutdownRelease(t *testing.T) {
	manager := newTestSessionManager(t, 1, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{}, nil
	})
	lease, err := manager.Acquire(context.Background(), "account-1", bookingapi.Credentials{Email: "one", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	session := lease.Session.(*testSession)
	manager.Close()
	if session.closed.Load() {
		t.Fatal("active session closed before its lease was released")
	}
	lease.Release()
	if !session.closed.Load() {
		t.Fatal("session was not closed after shutdown release")
	}
}

func newTestSessionManager(t *testing.T, max int, ttl time.Duration, factory func(context.Context, bookingapi.Credentials) (Session, error)) *SessionManager {
	t.Helper()
	manager, err := NewSessionManager(SessionFactoryFunc(factory), SessionManagerConfig{
		MaxEntries: max, IdleTTL: ttl, CacheKey: []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
