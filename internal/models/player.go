package models

import "time"

type Player struct {
	ID         int64     `json:"id"`
	TelegramID int64     `json:"telegram_id"`
	Username   *string   `json:"username"`
	FirstName  *string   `json:"first_name"`
	LastName   *string   `json:"last_name"`
	CreatedAt  time.Time `json:"created_at"`
}
