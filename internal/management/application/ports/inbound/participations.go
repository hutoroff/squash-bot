package inbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type ParticipationUseCases interface {
	Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error)
	Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error)
	AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	RemoveGuest(ctx context.Context, gameID, telegramID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
	GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
	GetRegisteredCount(ctx context.Context, gameID int64) (int, error)
	GetGuestCount(ctx context.Context, gameID int64) (int, error)
	KickPlayer(ctx context.Context, gameID, telegramID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
	KickGuestByID(ctx context.Context, gameID, guestID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
}
