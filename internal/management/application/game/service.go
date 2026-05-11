package game

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

type Service struct {
	gameRepo  outbound.GameRepository
	venueRepo outbound.VenueRepository
}

func NewService(gameRepo outbound.GameRepository, venueRepo outbound.VenueRepository) *Service {
	return &Service{gameRepo: gameRepo, venueRepo: venueRepo}
}

func (s *Service) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error) {
	if venueID != nil {
		if _, err := s.venueRepo.GetByIDAndGroupID(ctx, *venueID, chatID); err != nil {
			return nil, fmt.Errorf("venue %d does not belong to group %d", *venueID, chatID)
		}
	}

	courtsCount := len(strings.Split(courts, ","))
	game := &models.Game{
		ChatID:      chatID,
		GameDate:    gameDate,
		Courts:      courts,
		CourtsCount: courtsCount,
		VenueID:     venueID,
	}
	created, err := s.gameRepo.Create(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("create game: %w", err)
	}
	return s.gameRepo.GetByID(ctx, created.ID)
}

func (s *Service) UpdateMessageID(ctx context.Context, gameID, messageID int64) error {
	return s.gameRepo.UpdateMessageID(ctx, gameID, messageID)
}

func (s *Service) GetByID(ctx context.Context, id int64) (*models.Game, error) {
	return s.gameRepo.GetByID(ctx, id)
}

func (s *Service) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGames(ctx)
}

func (s *Service) GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGamesByChatIDs(ctx, chatIDs)
}

func (s *Service) GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error) {
	return s.gameRepo.GetNextGameForTelegramUser(ctx, telegramID)
}

func (s *Service) UpdateCourts(ctx context.Context, gameID int64, courts string) error {
	courtsCount := len(strings.Split(courts, ","))
	return s.gameRepo.UpdateCourts(ctx, gameID, courts, courtsCount)
}

func (s *Service) GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetGamesForPlayer(ctx, playerID)
}
