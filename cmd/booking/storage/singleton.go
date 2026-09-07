package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

const singletonLockKey = "squash-booking-executor:v1"

var ErrSingletonHeld = errors.New("booking execution singleton is already held")

// SingletonGuard owns a PostgreSQL session-level advisory lock on a dedicated
// connection. Losing that connection permanently closes Lost; it never tries
// to reacquire authority in the same process incarnation.
type SingletonGuard struct {
	conn     *pgx.Conn
	interval time.Duration
	lost     chan struct{}
	stop     chan struct{}
	done     chan struct{}

	healthy  atomic.Bool
	lostOnce sync.Once
	stopOnce sync.Once
	closeMu  sync.Mutex
	closed   bool
}

func AcquireSingletonGuard(ctx context.Context, databaseURL string, checkInterval time.Duration) (*SingletonGuard, error) {
	if checkInterval <= 0 {
		return nil, fmt.Errorf("singleton check interval must be positive")
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect booking singleton guard: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, singletonLockKey).Scan(&acquired); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("acquire booking singleton guard: %w", err)
	}
	if !acquired {
		_ = conn.Close(context.Background())
		return nil, ErrSingletonHeld
	}
	guard := &SingletonGuard{
		conn: conn, interval: checkInterval,
		lost: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}),
	}
	guard.healthy.Store(true)
	go guard.monitor()
	return guard, nil
}

func (g *SingletonGuard) Lost() <-chan struct{} { return g.lost }
func (g *SingletonGuard) Healthy() bool         { return g.healthy.Load() }
func (g *SingletonGuard) BackendPID() uint32    { return g.conn.PgConn().PID() }

func (g *SingletonGuard) monitor() {
	defer close(g.done)
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), g.interval)
			var one int
			err := g.conn.QueryRow(ctx, `SELECT 1`).Scan(&one)
			cancel()
			if err != nil || one != 1 {
				g.markLost()
				return
			}
		}
	}
}

func (g *SingletonGuard) markLost() {
	g.healthy.Store(false)
	g.lostOnce.Do(func() { close(g.lost) })
}

func (g *SingletonGuard) Close(ctx context.Context) error {
	g.closeMu.Lock()
	defer g.closeMu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	g.stopOnce.Do(func() { close(g.stop) })
	<-g.done

	var unlockErr error
	if g.healthy.Load() {
		var unlocked bool
		if err := g.conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, singletonLockKey).Scan(&unlocked); err != nil {
			unlockErr = fmt.Errorf("release booking singleton guard: %w", err)
		} else if !unlocked {
			unlockErr = fmt.Errorf("release booking singleton guard: lock was not held")
		}
	}
	g.healthy.Store(false)
	g.lostOnce.Do(func() { close(g.lost) })
	if err := g.conn.Close(ctx); err != nil && unlockErr == nil {
		return fmt.Errorf("close booking singleton connection: %w", err)
	}
	return unlockErr
}
