package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/cmd/management/storage"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type GameService struct {
	gameRepo  *storage.GameRepo
	venueRepo *storage.VenueRepo
}

func NewGameService(gameRepo *storage.GameRepo, venueRepo *storage.VenueRepo) *GameService {
	return &GameService{gameRepo: gameRepo, venueRepo: venueRepo}
}

func (s *GameService) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error) {
	// Verify venue ownership before inserting — prevents attaching another group's venue.
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
	// Re-fetch via GetByID so the returned game includes the hydrated Venue struct.
	return s.gameRepo.GetByID(ctx, created.ID)
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

// GetGamesForPlayer returns all games in which the player has any participation record.
func (s *GameService) GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetGamesForPlayer(ctx, playerID)
}
