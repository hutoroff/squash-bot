package storage

import (
	"context"
	"errors"

	"github.com/hutoroff/squash-bot/internal/featureflags"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FeatureFlagRepo struct{ pool *pgxpool.Pool }

func NewFeatureFlagRepo(pool *pgxpool.Pool) *FeatureFlagRepo { return &FeatureFlagRepo{pool: pool} }

type flagQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// One SELECT gives global and group overrides from one statement snapshot.
func readFlag(ctx context.Context, q flagQuerier, key featureflags.Key, groupID *int64) (featureflags.State, error) {
	d, err := featureflags.Lookup(key)
	if err != nil {
		return featureflags.State{}, err
	}
	rows, err := q.Query(ctx, `SELECT group_id, enabled FROM feature_flag_overrides
 WHERE key = $1 AND (group_id IS NULL OR group_id = $2)`, key, groupID)
	if err != nil {
		return featureflags.State{}, err
	}
	defer rows.Close()
	var global, group *bool
	for rows.Next() {
		var id *int64
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return featureflags.State{}, err
		}
		if id == nil {
			global = &enabled
		} else {
			group = &enabled
		}
	}
	if err := rows.Err(); err != nil {
		return featureflags.State{}, err
	}
	return featureflags.Resolve(d, global, group), nil
}
func (r *FeatureFlagRepo) Get(ctx context.Context, key featureflags.Key, groupID *int64) (featureflags.State, error) {
	return readFlag(ctx, r.pool, key, groupID)
}
func (r *FeatureFlagRepo) EnabledInTx(ctx context.Context, tx pgx.Tx, key featureflags.Key, groupID int64) (bool, error) {
	s, err := readFlag(ctx, tx, key, &groupID)
	return s.Enabled, err
}

// Set returns the previous override. nil enabled removes the override. Serialize
// writes per key so the returned old value (used for audit) is accurate.
func (r *FeatureFlagRepo) Set(ctx context.Context, key featureflags.Key, groupID *int64, enabled *bool) (*bool, error) {
	if _, err := featureflags.Lookup(key); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "feature_flag:"+string(key)); err != nil {
		return nil, err
	}
	var old *bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM feature_flag_overrides WHERE key = $1 AND group_id IS NOT DISTINCT FROM $2::bigint`, key, groupID).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if enabled == nil {
		_, err = tx.Exec(ctx, `DELETE FROM feature_flag_overrides WHERE key = $1 AND group_id IS NOT DISTINCT FROM $2::bigint`, key, groupID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO feature_flag_overrides (key, group_id, enabled) VALUES ($1, $2, $3)
 ON CONFLICT (key, group_id) DO UPDATE SET enabled = EXCLUDED.enabled`, key, groupID, *enabled)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return old, nil
}
