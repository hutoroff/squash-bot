package models

import "time"

type Game struct {
	ID                  int64
	ChatID              int64
	MessageID           *int64
	GameDate            time.Time
	CourtsCount         int
	NotifiedDayBefore   bool
	Completed           bool
	CreatedAt           time.Time
}
