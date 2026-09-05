package models

import "time"

// PlayerRating holds the current Glicko-2 rating for a player in a group.
type PlayerRating struct {
	GroupID     int64     `json:"group_id"`
	PlayerID    int64     `json:"player_id"`
	Rating      float64   `json:"rating"`
	RD          float64   `json:"rd"`
	Volatility  float64   `json:"volatility"`
	GamesPlayed int       `json:"games_played"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Player is optionally populated via JOIN.
	Player *Player `json:"player,omitempty"`
}

// RatingChange records the rating delta applied from a single game result.
type RatingChange struct {
	ID                int64     `json:"id"`
	GameResultID      int64     `json:"game_result_id"`
	GroupID           int64     `json:"group_id"`
	PlayerID          int64     `json:"player_id"`
	OldRating         float64   `json:"old_rating"`
	NewRating         float64   `json:"new_rating"`
	OldRD             float64   `json:"old_rd"`
	NewRD             float64   `json:"new_rd"`
	Delta             float64   `json:"delta"`
	AppliedAt         time.Time `json:"applied_at"`
	PolicyVersion     string    `json:"policy_version"`
	EvidenceWeight    float64   `json:"evidence_weight"`
	ScoreKind         ScoreKind `json:"score_kind,omitempty"`
	PolicyReason      string    `json:"policy_reason"`
	ScoreAwareEnabled bool      `json:"score_aware_enabled"`
}
