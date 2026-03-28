package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type GameService struct {
	gameRepo *storage.GameRepo
}

func NewGameService(gameRepo *storage.GameRepo) *GameService {
	return &GameService{gameRepo: gameRepo}
}

func (s *GameService) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string) (*models.Game, error) {
	courtsCount := len(strings.Split(courts, ","))
	game := &models.Game{
		ChatID:      chatID,
		GameDate:    gameDate,
		Courts:      courts,
		CourtsCount: courtsCount,
	}
	created, err := s.gameRepo.Create(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("create game: %w", err)
	}
	return created, nil
}

func (s *GameService) UpdateMessageID(ctx context.Context, gameID, messageID int64) error {
	return s.gameRepo.UpdateMessageID(ctx, gameID, messageID)
}

func (s *GameService) GetByID(ctx context.Context, id int64) (*models.Game, error) {
	return s.gameRepo.GetByID(ctx, id)
}

func (s *GameService) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGames(ctx)
}

func (s *GameService) GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGamesByChatIDs(ctx, chatIDs)
}

// GetNextGameForTelegramUser returns the nearest upcoming game where the user is registered.
// Returns nil, nil if the user has no upcoming registered games.
func (s *GameService) GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error) {
	return s.gameRepo.GetNextGameForTelegramUser(ctx, telegramID)
}

func (s *GameService) UpdateCourts(ctx context.Context, gameID int64, courts string) error {
	courtsCount := len(strings.Split(courts, ","))
	return s.gameRepo.UpdateCourts(ctx, gameID, courts, courtsCount)
}
