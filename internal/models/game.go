package models

import "time"

type Game struct {
	ID                int64     `json:"id"`
	ChatID            int64     `json:"chat_id"`
	MessageID         *int64    `json:"message_id"`
	GameDate          time.Time `json:"game_date"`
	CourtsCount       int       `json:"courts_count"`
	Courts            string    `json:"courts"`
	NotifiedDayBefore bool      `json:"notified_day_before"`
	Completed         bool      `json:"completed"`
	CreatedAt         time.Time `json:"created_at"`
}
