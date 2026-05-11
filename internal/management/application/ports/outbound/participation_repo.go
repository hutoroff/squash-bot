package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type ParticipationRepository interface {
	Upsert(ctx context.Context, gameID, playerID int64, status models.ParticipationStatus) error
	GetByGame(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
	DeleteByGameAndPlayer(ctx context.Context, gameID, playerID int64) (bool, error)
	GetRegisteredCount(ctx context.Context, gameID int64) (int, error)
}
