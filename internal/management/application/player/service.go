package player

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

// Service is a thin pass-through over PlayerRepository and GameRepository
// so the HTTP handler does not depend directly on the outbound adapter types.
type Service struct {
	playerRepo outbound.PlayerRepository
	gameRepo   outbound.GameRepository
}

func NewService(playerRepo outbound.PlayerRepository, gameRepo outbound.GameRepository) *Service {
	return &Service{playerRepo: playerRepo, gameRepo: gameRepo}
}

func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error) {
	return s.playerRepo.GetByTelegramID(ctx, telegramID)
}

func (s *Service) Upsert(ctx context.Context, p *models.Player) (*models.Player, error) {
	return s.playerRepo.Upsert(ctx, p)
}

func (s *Service) GetNextGame(ctx context.Context, telegramID int64) (*models.Game, error) {
	return s.gameRepo.GetNextGameForTelegramUser(ctx, telegramID)
}

func (s *Service) ListGames(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetGamesForPlayer(ctx, playerID)
}
