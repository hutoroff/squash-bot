package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

const (
	DefaultSessionMaxEntries = 32
	DefaultSessionIdleTTL    = 15 * time.Minute
)

var ErrSessionCapacity = errors.New("booking session cache capacity exhausted")

type Session any

type SessionFactory interface {
	NewSession(context.Context, bookingapi.Credentials) (Session, error)
}

type SessionFactoryFunc func(context.Context, bookingapi.Credentials) (Session, error)

func (f SessionFactoryFunc) NewSession(ctx context.Context, credentials bookingapi.Credentials) (Session, error) {
	return f(ctx, credentials)
}

type SessionManagerConfig struct {
	MaxEntries int
	IdleTTL    time.Duration
	Clock      func() time.Time
	CacheKey   []byte
}

type SessionManager struct {
	factory SessionFactory
	max     int
	idleTTL time.Duration
	now     func() time.Time
	key     []byte

	mu       sync.Mutex
	entries  map[string]*sessionEntry
	accounts map[string]*accountGate
	closed   bool
}

type sessionEntry struct {
	accountIdentity string
	session         Session
	lastUsed        time.Time
	pins            int
}

type accountGate struct {
	token chan struct{}
	refs  int
}

type SessionLease struct {
	manager     *SessionManager
	key         string
	account     string
	gate        *accountGate
	Session     Session
	once        sync.Once
	discardOnce sync.Once
}

func NewSessionManager(factory SessionFactory, cfg SessionManagerConfig) (*SessionManager, error) {
	if factory == nil {
		return nil, fmt.Errorf("session factory is required")
	}
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = DefaultSessionMaxEntries
	} else if cfg.MaxEntries < 0 {
		return nil, fmt.Errorf("session cache max entries must be positive")
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = DefaultSessionIdleTTL
	} else if cfg.IdleTTL < 0 {
		return nil, fmt.Errorf("session cache idle TTL must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if len(cfg.CacheKey) == 0 {
		cfg.CacheKey = make([]byte, 32)
		if _, err := rand.Read(cfg.CacheKey); err != nil {
			return nil, fmt.Errorf("generate process-local session cache key: %w", err)
		}
	}
	if len(cfg.CacheKey) < 16 {
		return nil, fmt.Errorf("session cache key must be at least 16 bytes")
	}
	return &SessionManager{
		factory: factory, max: cfg.MaxEntries, idleTTL: cfg.IdleTTL, now: cfg.Clock,
		key: append([]byte(nil), cfg.CacheKey...), entries: make(map[string]*sessionEntry),
		accounts: make(map[string]*accountGate),
	}, nil
}

// Acquire serializes all session-dependent work by durable account identity.
// Credential rotation changes only the disposable cache entry, never the lock
// domain. The returned lease must be released.
func (m *SessionManager) Acquire(ctx context.Context, accountIdentity string, credentials bookingapi.Credentials) (*SessionLease, error) {
	if accountIdentity == "" {
		return nil, fmt.Errorf("account identity is required")
	}
	gate, err := m.acquireAccount(ctx, accountIdentity)
	if err != nil {
		return nil, err
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			m.releaseAccount(accountIdentity, gate)
		}
	}()

	entryKey := accountIdentity + ":" + m.credentialVersion(credentials)
	now := m.now()
	var closeSessions []Session

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrCoordinatorClosed
	}
	closeSessions = append(closeSessions, m.removeExpiredLocked(now)...)
	for key, entry := range m.entries {
		if entry.accountIdentity == accountIdentity && key != entryKey && entry.pins == 0 {
			delete(m.entries, key)
			closeSessions = append(closeSessions, entry.session)
		}
	}
	if entry, ok := m.entries[entryKey]; ok {
		entry.pins++
		entry.lastUsed = now
		m.mu.Unlock()
		closeAll(closeSessions)
		releaseGate = false
		return &SessionLease{manager: m, key: entryKey, account: accountIdentity, gate: gate, Session: entry.session}, nil
	}
	for len(m.entries) >= m.max {
		key, entry := m.oldestIdleLocked()
		if entry == nil {
			m.mu.Unlock()
			closeAll(closeSessions)
			return nil, ErrSessionCapacity
		}
		delete(m.entries, key)
		closeSessions = append(closeSessions, entry.session)
	}
	entry := &sessionEntry{accountIdentity: accountIdentity, lastUsed: now, pins: 1}
	m.entries[entryKey] = entry // reserve capacity before constructing outside the mutex
	m.mu.Unlock()
	closeAll(closeSessions)

	session, err := m.factory.NewSession(ctx, credentials)
	if err != nil {
		m.mu.Lock()
		if m.entries[entryKey] == entry {
			delete(m.entries, entryKey)
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("create provider session: %w", err)
	}
	m.mu.Lock()
	if m.closed || m.entries[entryKey] != entry {
		if m.entries[entryKey] == entry {
			delete(m.entries, entryKey)
		}
		m.mu.Unlock()
		closeAll([]Session{session})
		if m.closed {
			return nil, ErrCoordinatorClosed
		}
		return nil, ErrSessionCapacity
	}
	entry.session = session
	entry.lastUsed = m.now()
	m.mu.Unlock()

	releaseGate = false
	return &SessionLease{manager: m, key: entryKey, account: accountIdentity, gate: gate, Session: session}, nil
}

// Discard removes and closes a suspect cached session without releasing the
// account gate. The executor still calls Release after all session-dependent
// work is finished.
func (l *SessionLease) Discard() {
	if l == nil || l.manager == nil {
		return
	}
	l.discardOnce.Do(func() {
		var closeSession Session
		l.manager.mu.Lock()
		if entry := l.manager.entries[l.key]; entry != nil {
			delete(l.manager.entries, l.key)
			closeSession = entry.session
		}
		l.manager.mu.Unlock()
		closeAll([]Session{closeSession})
	})
}

func (l *SessionLease) Release() {
	if l == nil || l.manager == nil {
		return
	}
	l.once.Do(func() {
		var closeSession Session
		l.manager.mu.Lock()
		if entry := l.manager.entries[l.key]; entry != nil && entry.pins > 0 {
			entry.pins--
			entry.lastUsed = l.manager.now()
			if l.manager.closed && entry.pins == 0 {
				delete(l.manager.entries, l.key)
				closeSession = entry.session
			}
		}
		l.manager.mu.Unlock()
		closeAll([]Session{closeSession})
		l.manager.releaseAccount(l.account, l.gate)
	})
}

func (m *SessionManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	sessions := make([]Session, 0, len(m.entries))
	for key, entry := range m.entries {
		if entry.pins == 0 {
			sessions = append(sessions, entry.session)
			delete(m.entries, key)
		}
	}
	m.mu.Unlock()
	closeAll(sessions)
}

func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func (m *SessionManager) acquireAccount(ctx context.Context, account string) (*accountGate, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrCoordinatorClosed
	}
	gate := m.accounts[account]
	if gate == nil {
		gate = &accountGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		m.accounts[account] = gate
	}
	gate.refs++
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		m.mu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(m.accounts, account)
		}
		m.mu.Unlock()
		return nil, ctx.Err()
	case <-gate.token:
		return gate, nil
	}
}

func (m *SessionManager) releaseAccount(account string, gate *accountGate) {
	gate.token <- struct{}{}
	m.mu.Lock()
	gate.refs--
	if gate.refs == 0 {
		delete(m.accounts, account)
	}
	m.mu.Unlock()
}

func (m *SessionManager) credentialVersion(credentials bookingapi.Credentials) string {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(credentials.Email))))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(credentials.Password))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *SessionManager) removeExpiredLocked(now time.Time) []Session {
	var removed []Session
	for key, entry := range m.entries {
		if entry.pins == 0 && now.Sub(entry.lastUsed) >= m.idleTTL {
			delete(m.entries, key)
			removed = append(removed, entry.session)
		}
	}
	return removed
}

func (m *SessionManager) oldestIdleLocked() (string, *sessionEntry) {
	var oldestKey string
	var oldest *sessionEntry
	for key, entry := range m.entries {
		if entry.pins != 0 || entry.session == nil {
			continue
		}
		if oldest == nil || entry.lastUsed.Before(oldest.lastUsed) {
			oldestKey, oldest = key, entry
		}
	}
	return oldestKey, oldest
}

func closeAll(sessions []Session) {
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if closer, ok := session.(io.Closer); ok && closer != nil {
			_ = closer.Close()
		}
	}
}
