package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type ParticipationService struct {
	playerRepo        *storage.PlayerRepo
	participationRepo *storage.ParticipationRepo
}

func NewParticipationService(playerRepo *storage.PlayerRepo, participationRepo *storage.ParticipationRepo) *ParticipationService {
	return &ParticipationService{
		playerRepo:        playerRepo,
		participationRepo: participationRepo,
	}
}

func (s *ParticipationService) Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error) {
	player := &models.Player{TelegramID: telegramID}
	if username != "" {
		player.Username = &username
	}
	if firstName != "" {
		player.FirstName = &firstName
	}
	if lastName != "" {
		player.LastName = &lastName
	}

	saved, err := s.playerRepo.Upsert(ctx, player)
	if err != nil {
		return nil, fmt.Errorf("upsert player: %w", err)
	}

	if err := s.participationRepo.Upsert(ctx, gameID, saved.ID, models.StatusRegistered); err != nil {
		return nil, fmt.Errorf("upsert participation: %w", err)
	}

	slog.Info("Player joined", "player", displayName(username, firstName, lastName), "game_id", gameID)

	return s.participationRepo.GetByGame(ctx, gameID)
}

// Skip marks a player as skipped. Returns (participations, skipped, error).
// skipped=false means the player was not registered, so no change was made.
func (s *ParticipationService) Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error) {
	existingPlayer, err := s.playerRepo.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get player: %w", err)
	}

	participations, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, false, fmt.Errorf("get participations: %w", err)
	}

	registered := false
	for _, p := range participations {
		if p.PlayerID == existingPlayer.ID && p.Status == models.StatusRegistered {
			registered = true
			break
		}
	}
	if !registered {
		return nil, false, nil
	}

	if err := s.participationRepo.Upsert(ctx, gameID, existingPlayer.ID, models.StatusSkipped); err != nil {
		return nil, false, fmt.Errorf("upsert participation: %w", err)
	}

	slog.Info("Player skipped", "player", displayName(username, firstName, lastName), "game_id", gameID)

	updated, err := s.participationRepo.GetByGame(ctx, gameID)
	return updated, true, err
}

func displayName(username, firstName, lastName string) string {
	if username != "" {
		return "@" + username
	}
	name := firstName
	if lastName != "" {
		if name != "" {
			name += " "
		}
		name += lastName
	}
	return name
}
