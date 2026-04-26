package storage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ServiceStateRepo stores and retrieves arbitrary key-value state for the service.
type ServiceStateRepo struct {
	pool *pgxpool.Pool
}

func NewServiceStateRepo(pool *pgxpool.Pool) *ServiceStateRepo {
	return &ServiceStateRepo{pool: pool}
}

// Get returns the value for the given key, or pgx.ErrNoRows if not set.
func (r *ServiceStateRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM service_state WHERE key = $1`, key,
	).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

// Set inserts or updates the value for the given key.
func (r *ServiceStateRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO service_state (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, value,
	)
	return err
}
