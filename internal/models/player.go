package models

import "time"

type Player struct {
	ID int64 `json:"id"`
	// UserID is nil until PlayerRepo is rekeyed in Step 3 — no query
	// populates it yet, and a bare int64 would publish a fake user_id: 0.
	UserID *int64 `json:"user_id,omitempty"`
	// TelegramID/Username/FirstName/LastName are derived from the player's
	// telegram user_identity (hydrated via JOIN once storage is rekeyed in
	// Step 3) and kept here so JSON shape, gameformat, and DM sending stay
	// unchanged.
	TelegramID int64     `json:"telegram_id"`
	Username   *string   `json:"username"`
	FirstName  *string   `json:"first_name"`
	LastName   *string   `json:"last_name"`
	CreatedAt  time.Time `json:"created_at"`
}
