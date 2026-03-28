package models

import "time"

// GuestParticipation represents a non-Telegram guest invited to a game by a registered player.
// Each row corresponds to one "+1" added by the inviter.
type GuestParticipation struct {
	ID                int64     `json:"id"`
	GameID            int64     `json:"game_id"`
	InvitedByPlayerID int64     `json:"invited_by_player_id"`
	InvitedBy         *Player   `json:"invited_by"` // populated via JOIN in GuestRepo.GetByGame
	CreatedAt         time.Time `json:"created_at"`
}
