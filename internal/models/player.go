package models

import "time"

type Player struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
	// TelegramID/Username/FirstName/LastName are derived from the player's
	// telegram user_identity (hydrated via JOIN in PlayerRepo) so JSON shape,
	// gameformat, and DM sending need no changes.
	TelegramID int64     `json:"telegram_id"`
	Username   *string   `json:"username"`
	FirstName  *string   `json:"first_name"`
	LastName   *string   `json:"last_name"`
	CreatedAt  time.Time `json:"created_at"`
}
