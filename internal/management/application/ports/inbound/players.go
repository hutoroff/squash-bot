package inbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type PlayerUseCases interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error)
	Upsert(ctx context.Context, player *models.Player) (*models.Player, error)
	GetNextGame(ctx context.Context, telegramID int64) (*models.Game, error)
	ListGames(ctx context.Context, playerID int64) ([]models.PlayerGame, error)
}
