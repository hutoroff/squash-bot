package models

import "time"

type ParticipationStatus string

const (
	StatusRegistered ParticipationStatus = "registered"
	StatusSkipped    ParticipationStatus = "skipped"
)

type GameParticipation struct {
	ID        int64
	GameID    int64
	PlayerID  int64
	Player    *Player
	Status    ParticipationStatus
	CreatedAt time.Time
}
