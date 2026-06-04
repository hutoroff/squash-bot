package storage

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserPreferencesRepo struct {
	pool *pgxpool.Pool
}

func NewUserPreferencesRepo(pool *pgxpool.Pool) *UserPreferencesRepo {
	return &UserPreferencesRepo{pool: pool}
}

// GetByTelegramID returns the preferences for the given user.
// Returns pgx.ErrNoRows if no row exists.
func (r *UserPreferencesRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*models.UserPreferences, error) {
	const q = `
		SELECT telegram_id, dm_language, created_at, updated_at
		FROM user_preferences WHERE telegram_id = $1`
	row := r.pool.QueryRow(ctx, q, telegramID)
	var p models.UserPreferences
	if err := row.Scan(&p.TelegramID, &p.DMLanguage, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// SetDMLanguage upserts the dm_language for the given user.
func (r *UserPreferencesRepo) SetDMLanguage(ctx context.Context, telegramID int64, language string) error {
	const q = `
		INSERT INTO user_preferences (telegram_id, dm_language)
		VALUES ($1, $2)
		ON CONFLICT (telegram_id) DO UPDATE
		    SET dm_language = EXCLUDED.dm_language,
		        updated_at  = NOW()`
	_, err := r.pool.Exec(ctx, q, telegramID, language)
	return err
}
