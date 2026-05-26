package storage

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GroupRepo struct {
	pool *pgxpool.Pool
}

func NewGroupRepo(pool *pgxpool.Pool) *GroupRepo {
	return &GroupRepo{pool: pool}
}

// Upsert inserts or updates a group record.
// The language and timezone columns are preserved on conflict (only title and bot_is_admin are updated).
func (r *GroupRepo) Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO bot_groups (chat_id, title, bot_is_admin, auto_booking_allowed)
		 VALUES ($1, $2, $3, FALSE)
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

// SetTimezone updates the stored IANA timezone for a group.
// Returns pgx.ErrNoRows if no group with that chat ID exists.
func (r *GroupRepo) SetTimezone(ctx context.Context, chatID int64, timezone string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE bot_groups SET timezone = $1 WHERE chat_id = $2`,
		timezone, chatID,
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
		`SELECT chat_id, title, bot_is_admin, language, timezone, changelog_enabled, auto_booking_allowed, added_at FROM bot_groups WHERE chat_id = $1`, chatID,
	).Scan(&g.ChatID, &g.Title, &g.BotIsAdmin, &g.Language, &g.Timezone, &g.ChangelogEnabled, &g.AutoBookingAllowed, &g.AddedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GetAll returns all groups the bot is currently a member of.
func (r *GroupRepo) GetAll(ctx context.Context) ([]models.Group, error) {
	rows, err := r.pool.Query(ctx, `SELECT chat_id, title, bot_is_admin, language, timezone, changelog_enabled, auto_booking_allowed, added_at FROM bot_groups ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ChatID, &g.Title, &g.BotIsAdmin, &g.Language, &g.Timezone, &g.ChangelogEnabled, &g.AutoBookingAllowed, &g.AddedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// SetChangelogEnabled updates the changelog_enabled flag for a group.
// Returns pgx.ErrNoRows if no group with that chat ID exists.
func (r *GroupRepo) SetChangelogEnabled(ctx context.Context, chatID int64, enabled bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE bot_groups SET changelog_enabled = $1 WHERE chat_id = $2`,
		enabled, chatID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetAutoBookingAllowed atomically sets the auto_booking_allowed flag on a group.
// When allowed is false, it also cascades by disabling auto_booking_enabled on all venues
// in that group, returning the IDs of affected venues.
// Returns pgx.ErrNoRows if no group with that chat ID exists.
func (r *GroupRepo) SetAutoBookingAllowed(ctx context.Context, chatID int64, allowed bool) ([]int64, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`UPDATE bot_groups SET auto_booking_allowed = $1 WHERE chat_id = $2`,
		allowed, chatID,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}

	var cascadedIDs []int64
	if !allowed {
		rows, err := tx.Query(ctx,
			`UPDATE venues SET auto_booking_enabled = FALSE WHERE group_id = $1 AND auto_booking_enabled = TRUE RETURNING id`,
			chatID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			cascadedIDs = append(cascadedIDs, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return cascadedIDs, nil
}
