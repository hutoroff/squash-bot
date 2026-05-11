package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type PlayerRepository interface {
	Upsert(ctx context.Context, player *models.Player) (*models.Player, error)
	GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error)
}
