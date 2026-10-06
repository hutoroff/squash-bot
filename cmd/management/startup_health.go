package main

import (
	"context"
	"sync/atomic"

	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/jackc/pgx/v5/pgxpool"
)

// managementHealthSource keeps startup readiness false until migrations and the
// business listener are complete. The pool pointer is published atomically so
// the admin listener can safely start before database initialization.
type managementHealthSource struct {
	pool               atomic.Pointer[pgxpool.Pool]
	migrationsComplete atomic.Bool
	businessListening  atomic.Bool
	draining           atomic.Bool
}

func (s *managementHealthSource) readiness(ctx context.Context) observability.Readiness {
	pool := s.pool.Load()
	database := pool != nil && s.migrationsComplete.Load() && pool.Ping(ctx) == nil
	listening := s.businessListening.Load()
	return observability.Readiness{
		Ready: database && listening && !s.draining.Load(),
		Checks: map[string]bool{
			"database":          database,
			"business_listener": listening,
			"draining":          !s.draining.Load(),
		},
	}
}
