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

// AutoBookingResultRepository is the data access interface for auto-booking results.
type AutoBookingResultRepository interface {
	// Save persists the courts booked by AutoBookingJob for a venue on a specific game date and time slot.
	// Silently ignores duplicate entries (same venue_id + game_date + game_time).
	Save(ctx context.Context, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int) error
	// GetByVenueAndDate returns all stored results for the given venue and game date,
	// ordered by game_time ASC. Returns an empty (non-nil) slice when none exist.
	GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.AutoBookingResult, error)
	// GetByVenueAndDateAndTime returns the result for an exact (venue, date, time) combination,
	// or (nil, nil) when no row exists. Used by AutoBookingJob for per-slot dedup.
	GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) (*models.AutoBookingResult, error)
	// GetByGameID returns the result linked to the given game, or (nil, nil) if none.
	// Used by CancellationReminderJob to find the time slot for a specific game.
	GetByGameID(ctx context.Context, gameID int64) (*models.AutoBookingResult, error)
	// SetGameID links an auto-booking result to the Telegram game created by BookingReminderJob.
	SetGameID(ctx context.Context, resultID, gameID int64) error
}

// CourtBookingRepository is the data access interface for per-court booking records.
// Each entry links a booked court to the credential used, enabling credential-aware cancellation.
type CourtBookingRepository interface {
	// Save inserts a new court booking record. Silently ignores duplicates by match_id.
	Save(ctx context.Context, booking *models.CourtBooking) error
	// GetByVenueAndDate returns active (non-canceled) bookings for the venue and date.
	GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.CourtBooking, error)
	// GetByVenueAndDateAndTime returns active bookings filtered by game time slot.
	// Falls back to game_time='' rows (legacy) when gameTime is non-empty and no time-specific rows exist.
	GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) ([]*models.CourtBooking, error)
	// MarkCanceled soft-deletes the booking by setting canceled_at to NOW().
	MarkCanceled(ctx context.Context, matchID string) error
	// HasActiveByCredentialID returns true if any non-canceled booking uses the credential.
	HasActiveByCredentialID(ctx context.Context, credentialID int64) (bool, error)
	// HasActiveByVenueID returns true if any non-canceled booking exists for the venue.
	HasActiveByVenueID(ctx context.Context, venueID int64) (bool, error)
	// MarkCanceledByVenueAndDate soft-deletes all active bookings for the venue on the given date.
	// Called by DayAfterCleanupJob to close out kept bookings after a game completes.
	MarkCanceledByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) error
}

// VenueCredentialRepository is the data access interface for venue booking credentials.
// Passwords are stored encrypted; this interface never exposes raw passwords.
type VenueCredentialRepository interface {
	// Create inserts a new credential. enc_password must already be encrypted.
	Create(ctx context.Context, venueID int64, login, encPassword string, priority, maxCourts int) (*models.VenueCredential, error)
	// ListByVenueID returns all credentials for a venue ordered by priority ASC.
	// EncryptedPassword is NOT populated — use ListWithPasswordByVenueID for booking.
	ListByVenueID(ctx context.Context, venueID int64) ([]*models.VenueCredential, error)
	// ListWithPasswordByVenueID returns all credentials including EncryptedPassword,
	// ordered by priority ASC. Only for internal scheduler use.
	ListWithPasswordByVenueID(ctx context.Context, venueID int64) ([]*models.VenueCredential, error)
	// GetWithPasswordByID returns a single credential including EncryptedPassword.
	// Used by VenueCredentialService.GetDecryptedByID for per-court cancellation.
	GetWithPasswordByID(ctx context.Context, id int64) (*models.VenueCredential, error)
	// Delete removes a credential scoped to venueID (prevents cross-venue deletions).
	Delete(ctx context.Context, id, venueID int64) error
	// ExistsByLogin reports whether a credential with the given login already exists for the venue.
	ExistsByLogin(ctx context.Context, venueID int64, login string) (bool, error)
	// PrioritiesInUse returns all priority values currently in use for the venue.
	PrioritiesInUse(ctx context.Context, venueID int64) ([]int, error)
	// SetLastErrorAt records the current timestamp as the last error time for a credential.
	SetLastErrorAt(ctx context.Context, id int64) error
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
