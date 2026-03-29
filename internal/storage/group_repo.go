package storage

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type GroupRepo struct {
	pool *pgxpool.Pool
}

func NewGroupRepo(pool *pgxpool.Pool) *GroupRepo {
	return &GroupRepo{pool: pool}
}

// Upsert inserts or updates a group record.
// The language column is preserved on conflict (only title and bot_is_admin are updated).
func (r *GroupRepo) Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO bot_groups (chat_id, title, bot_is_admin)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (chat_id) DO UPDATE SET title = EXCLUDED.title, bot_is_admin = EXCLUDED.bot_is_admin`,
		chatID, title, botIsAdmin,
	)
	return err
}

// SetLanguage updates the stored language code for a group.
// Returns pgx.ErrNoRows if no group with that chat ID exists.
func (r *GroupRepo) SetLanguage(ctx context.Context, chatID int64, language string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE bot_groups SET language = $1 WHERE chat_id = $2`,
		language, chatID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Remove deletes a group record (bot left or was kicked).
func (r *GroupRepo) Remove(ctx context.Context, chatID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM bot_groups WHERE chat_id = $1`, chatID)
	return err
}

// Exists reports whether a group with the given chat ID is registered.
func (r *GroupRepo) Exists(ctx context.Context, chatID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM bot_groups WHERE chat_id = $1)`, chatID,
	).Scan(&exists)
	return exists, err
}

// GetByID returns the group with the given chat ID, or nil if not found.
func (r *GroupRepo) GetByID(ctx context.Context, chatID int64) (*models.Group, error) {
	var g models.Group
	err := r.pool.QueryRow(ctx,
		`SELECT chat_id, title, bot_is_admin, language FROM bot_groups WHERE chat_id = $1`, chatID,
	).Scan(&g.ChatID, &g.Title, &g.BotIsAdmin, &g.Language)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GetAll returns all groups the bot is currently a member of.
func (r *GroupRepo) GetAll(ctx context.Context) ([]models.Group, error) {
	rows, err := r.pool.Query(ctx, `SELECT chat_id, title, bot_is_admin, language FROM bot_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ChatID, &g.Title, &g.BotIsAdmin, &g.Language); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}
