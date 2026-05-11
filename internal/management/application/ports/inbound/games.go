package inbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type GameUseCases interface {
	CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error)
	GetByID(ctx context.Context, id int64) (*models.Game, error)
	GetUpcomingGames(ctx context.Context) ([]*models.Game, error)
	GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error)
	UpdateMessageID(ctx context.Context, gameID, messageID int64) error
	UpdateCourts(ctx context.Context, gameID int64, courts string) error
}
