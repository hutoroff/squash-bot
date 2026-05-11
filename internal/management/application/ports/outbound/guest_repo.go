package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type GuestRepository interface {
	AddGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error)
	RemoveLatestGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error)
	GetByGame(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
	DeleteByID(ctx context.Context, gameID, guestID int64) (bool, error)
	GetCountByGame(ctx context.Context, gameID int64) (int, error)
}
