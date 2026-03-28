package models

import "time"

// GuestParticipation represents a non-Telegram guest invited to a game by a registered player.
// Each row corresponds to one "+1" added by the inviter.
type GuestParticipation struct {
	ID                  int64
	GameID              int64
	InvitedByPlayerID   int64
	InvitedBy           *Player // populated via JOIN in GuestRepo.GetByGame
	CreatedAt           time.Time
}
