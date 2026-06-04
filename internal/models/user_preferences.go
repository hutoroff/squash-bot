package models

import "time"

// UserPreferences holds per-user DM settings stored independently of the players table.
type UserPreferences struct {
	TelegramID    int64     `json:"telegram_id"`
	DMLanguage    string    `json:"dm_language"`
	ResultsOptOut bool      `json:"results_opt_out"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
