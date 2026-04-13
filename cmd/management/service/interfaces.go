package service

import (
	"context"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// TelegramAPI is the subset of the Telegram Bot API used by service-layer types.
// *tgbotapi.BotAPI satisfies this interface.
type TelegramAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetChatAdministrators(config tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error)
}

// Notifier edits the Telegram group message for a game to reflect current participation state.
type Notifier interface {
	EditGameMessage(ctx context.Context, gameID int64)
}

// GameRepository is the data access interface for games.
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

// PlayerRepository is the data access interface for players.
type PlayerRepository interface {
	Upsert(ctx context.Context, player *models.Player) (*models.Player, error)
	GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error)
}

// ParticipationRepository is the data access interface for game participations.
type ParticipationRepository interface {
	Upsert(ctx context.Context, gameID, playerID int64, status models.ParticipationStatus) error
	GetByGame(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
	DeleteByGameAndPlayer(ctx context.Context, gameID, playerID int64) (bool, error)
	GetRegisteredCount(ctx context.Context, gameID int64) (int, error)
}

// GuestRepository is the data access interface for guest participations.
type GuestRepository interface {
	AddGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error)
	RemoveLatestGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error)
	GetByGame(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
	DeleteByID(ctx context.Context, gameID, guestID int64) (bool, error)
	GetCountByGame(ctx context.Context, gameID int64) (int, error)
}

// GroupRepository is the data access interface for bot groups.
type GroupRepository interface {
	Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error
	SetLanguage(ctx context.Context, chatID int64, language string) error
	SetTimezone(ctx context.Context, chatID int64, timezone string) error
	Remove(ctx context.Context, chatID int64) error
	Exists(ctx context.Context, chatID int64) (bool, error)
	GetByID(ctx context.Context, chatID int64) (*models.Group, error)
	GetAll(ctx context.Context) ([]models.Group, error)
}

// VenueRepository is the data access interface for venues.
type VenueRepository interface {
	Create(ctx context.Context, venue *models.Venue) (*models.Venue, error)
	GetByID(ctx context.Context, id int64) (*models.Venue, error)
	GetByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error)
	GetByGroupID(ctx context.Context, groupID int64) ([]*models.Venue, error)
	Update(ctx context.Context, venue *models.Venue) (*models.Venue, error)
	Delete(ctx context.Context, id, groupID int64) error
	SetLastBookingReminderAt(ctx context.Context, venueID int64) error
	SetLastAutoBookingAt(ctx context.Context, venueID int64) error
}
