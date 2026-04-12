package models

import "time"

// PlayerGame is a read-only aggregated view returned by GET /api/v1/players/{id}/games.
// It combines game data with the player's participation status, the group's timezone,
// and the current registered-player count — exactly the fields the web frontend needs.
type PlayerGame struct {
	ID                  int64     `json:"id"`
	GameDate            time.Time `json:"game_date"`
	CourtsCount         int       `json:"courts_count"`
	Courts              string    `json:"courts"`
	Completed           bool      `json:"completed"`
	ParticipationStatus string    `json:"participation_status"`
	// ParticipantCount is the total number of registered players plus guests.
	ParticipantCount int    `json:"participant_count"`
	VenueName        string `json:"venue_name,omitempty"`
	VenueAddress     string `json:"venue_address,omitempty"`
	GroupTitle       string `json:"group_title"`
	Timezone         string `json:"timezone"`
}

type Game struct {
	ID                int64     `json:"id"`
	ChatID            int64     `json:"chat_id"`
	MessageID         *int64    `json:"message_id"`
	GameDate          time.Time `json:"game_date"`
	CourtsCount       int       `json:"courts_count"`
	Courts            string    `json:"courts"`
	VenueID           *int64    `json:"venue_id,omitempty"`
	Venue             *Venue    `json:"venue,omitempty"`
	NotifiedDayBefore bool      `json:"notified_day_before"`
	Completed         bool      `json:"completed"`
	CreatedAt         time.Time `json:"created_at"`
}
