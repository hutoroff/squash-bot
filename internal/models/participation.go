package models

import "time"

type ParticipationStatus string

const (
	StatusRegistered ParticipationStatus = "registered"
	StatusSkipped    ParticipationStatus = "skipped"
)

type GameParticipation struct {
	ID        int64               `json:"id"`
	GameID    int64               `json:"game_id"`
	PlayerID  int64               `json:"player_id"`
	Player    *Player             `json:"player"`
	Status    ParticipationStatus `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
}
