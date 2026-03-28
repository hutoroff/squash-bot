package storage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BotGroup struct {
	ChatID     int64
	Title      string
	BotIsAdmin bool
}

type GroupRepo struct {
	pool *pgxpool.Pool
}

func NewGroupRepo(pool *pgxpool.Pool) *GroupRepo {
	return &GroupRepo{pool: pool}
}

// Upsert inserts or updates a group record.
func (r *GroupRepo) Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO bot_groups (chat_id, title, bot_is_admin)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (chat_id) DO UPDATE SET title = EXCLUDED.title, bot_is_admin = EXCLUDED.bot_is_admin`,
		chatID, title, botIsAdmin,
	)
	return err
}

// Remove deletes a group record (bot left or was kicked).
func (r *GroupRepo) Remove(ctx context.Context, chatID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM bot_groups WHERE chat_id = $1`, chatID)
	return err
}

// GetAll returns all groups the bot is currently a member of.
func (r *GroupRepo) GetAll(ctx context.Context) ([]BotGroup, error) {
	rows, err := r.pool.Query(ctx, `SELECT chat_id, title, bot_is_admin FROM bot_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []BotGroup
	for rows.Next() {
		var g BotGroup
		if err := rows.Scan(&g.ChatID, &g.Title, &g.BotIsAdmin); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}
