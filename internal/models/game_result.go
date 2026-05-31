package models

import "time"

type GameResultStatus string

const (
	GameResultPending      GameResultStatus = "pending"
	GameResultApproved     GameResultStatus = "approved"
	GameResultAutoApproved GameResultStatus = "auto_approved"
	GameResultRejected     GameResultStatus = "rejected"
	GameResultCanceled     GameResultStatus = "canceled"
)

type GameResult struct {
	ID                int64            `json:"id"`
	GameID            int64            `json:"game_id"`
	GroupID           int64            `json:"group_id"`
	AuthorID          int64            `json:"author_id"`
	OpponentID        int64            `json:"opponent_id"`
	WinnerID          *int64           `json:"winner_id,omitempty"`
	Score             string           `json:"score"`
	Status            GameResultStatus `json:"status"`
	SubmittedAt       time.Time        `json:"submitted_at"`
	DecidedAt         *time.Time       `json:"decided_at,omitempty"`
	ApprovalChatID    *int64           `json:"approval_chat_id,omitempty"`
	ApprovalMessageID *int             `json:"approval_message_id,omitempty"`
	// AutoApproveAt is not persisted; computed as SubmittedAt + 48h and returned in POST response.
	AutoApproveAt *time.Time `json:"auto_approve_at,omitempty"`
	// Author and Opponent are not persisted; populated by the service layer for HTTP responses
	// so clients can DM either player without a separate lookup.
	Author   *Player `json:"author,omitempty"`
	Opponent *Player `json:"opponent,omitempty"`
}
