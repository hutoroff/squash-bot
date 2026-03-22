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
