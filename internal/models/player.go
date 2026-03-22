package models

import "time"

type Player struct {
	ID         int64
	TelegramID int64
	Username   *string
	FirstName  *string
	LastName   *string
	CreatedAt  time.Time
}
