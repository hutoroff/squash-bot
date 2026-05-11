package outbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type GameRepository interface {
	Create(ctx context.Context, game *models.Game) (*models.Game, error)
	GetByID(ctx context.Context, id int64) (*models.Game, error)
	GetUpcomingGames(ctx context.Context) ([]*models.Game, error)
	GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error)
	UpdateMessageID(ctx context.Context, gameID, messageID int64) error
	UpdateCourts(ctx context.Context, gameID int64, courts string, courtsCount int) error
	GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error)
	GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error)
	GetUpcomingUnnotifiedGames(ctx context.Context) ([]*models.Game, error)
	GetUncompletedGamesByGroupAndDay(ctx context.Context, chatID int64, from, to time.Time) ([]*models.Game, error)
	MarkNotifiedDayBefore(ctx context.Context, gameID int64) error
	MarkCompleted(ctx context.Context, gameID int64) error
}
