package service

import (
	"context"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
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
	GetNextGameForUser(ctx context.Context, userID int64) (*models.Game, error)
	GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error)
	GetUpcomingUnnotifiedGames(ctx context.Context) ([]*models.Game, error)
	GetUncompletedGamesByGroupAndDay(ctx context.Context, chatID int64, from, to time.Time) ([]*models.Game, error)
	// GetCompletedGamesByGroupAndDay returns completed games for a group whose
	// game_date falls in [from, to). Used by PostLeaderboardJob to gate posting
	// on "24 h after the day's last game start".
	GetCompletedGamesByGroupAndDay(ctx context.Context, chatID int64, from, to time.Time) ([]*models.Game, error)
	MarkNotifiedDayBefore(ctx context.Context, gameID int64) error
	MarkCompleted(ctx context.Context, gameID int64) error
	// GetUpcomingGamesForFinalCheck returns future uncompleted games where
	// final_court_check_done is false, with venue data joined.
	GetUpcomingGamesForFinalCheck(ctx context.Context) ([]*models.Game, error)
	// MarkFinalCourtCheckDone sets final_court_check_done = true for the given game.
	MarkFinalCourtCheckDone(ctx context.Context, gameID int64) error
	// GetUpcomingGamesForHalfwayCheck returns future uncompleted games where
	// halfway_court_check_done is false, with venue data joined.
	GetUpcomingGamesForHalfwayCheck(ctx context.Context) ([]*models.Game, error)
	// MarkHalfwayCourtCheckDone sets halfway_court_check_done = true for the given game.
	MarkHalfwayCourtCheckDone(ctx context.Context, gameID int64) error
	// GetRecentCompletedGamesForPlayer returns past games for a user in a
	// specific group within the result-submission window (`days`), ignoring the
	// completed flag.
	GetRecentCompletedGamesForPlayer(ctx context.Context, userID, groupID int64, days int) ([]models.PlayerGame, error)
	// GameInResultWindow reports whether a result may still be submitted for the game,
	// i.e. its local day (group timezone) is today or up to `days` days ago.
	GameInResultWindow(ctx context.Context, gameID int64, days int) (bool, error)
	// ListGroupIDsForPlayer returns the distinct chat_ids of groups where the player
	// has at least one participation record. Used by the leaderboard group picker.
	ListGroupIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error)
	// PlayerCanAccessGame reports whether the user has any participation
	// record (registered or skipped) in any game within the same chat as gameID's
	// game — i.e. whether the caller is associated with that game's group. Used
	// to authorize the web service's per-game endpoints (IDOR guard).
	PlayerCanAccessGame(ctx context.Context, userID, gameID int64) (bool, error)
}

// PlayerRepository is the data access interface for players. players stay
// lazy — Upsert only creates a row on first join.
type PlayerRepository interface {
	Upsert(ctx context.Context, userID int64) (*models.Player, error)
	GetByUserID(ctx context.Context, userID int64) (*models.Player, error)
	GetByID(ctx context.Context, id int64) (*models.Player, error)
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
	SetChangelogEnabled(ctx context.Context, chatID int64, enabled bool) error
	SetLeaderboardNotificationsEnabled(ctx context.Context, chatID int64, enabled bool) error
	SetAutoBookingAllowed(ctx context.Context, chatID int64, allowed bool) (cascadedVenueIDs []int64, err error)
	Remove(ctx context.Context, chatID int64) error
	Exists(ctx context.Context, chatID int64) (bool, error)
	GetByID(ctx context.Context, chatID int64) (*models.Group, error)
	GetAll(ctx context.Context) ([]models.Group, error)
	SetLastLeaderboardPostedFor(ctx context.Context, chatID int64, date time.Time) error
}

// ServiceStateRepository stores and retrieves arbitrary key-value state for the service.
type ServiceStateRepository interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// AutoBookingResultRepository is the data access interface for auto-booking results.
type AutoBookingResultRepository interface {
	// Save persists the courts booked by AutoBookingJob for a venue on a specific game date and time slot.
	// Returns the row id of the inserted (or upserted) record.
	Save(ctx context.Context, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int) (int64, error)
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
	// GetActiveByVenueDateAndLabels returns active bookings for the venue+date whose court_label
	// matches one of the provided labels. Used by the manual court-removal flow.
	GetActiveByVenueDateAndLabels(ctx context.Context, venueID int64, gameDate time.Time, labels []string) ([]*models.CourtBooking, error)
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

// GameResultRepository is the data access interface for game results.
type GameResultRepository interface {
	Create(ctx context.Context, r *models.GameResult) (int64, error)
	GetByID(ctx context.Context, id int64) (*models.GameResult, error)
	SetApprovalMessage(ctx context.Context, id, chatID int64, messageID int) error
	// Decide transitions a pending result to approved/auto_approved/rejected/canceled.
	// Returns ErrGameResultNotPending if the row is not currently pending.
	Decide(ctx context.Context, id int64, status models.GameResultStatus, decidedAt time.Time) error
	// DecideInTx is the same as Decide but runs inside the caller-provided transaction,
	// so the status flip can be made atomic with downstream rating updates.
	DecideInTx(ctx context.Context, tx pgx.Tx, id int64, status models.GameResultStatus, decidedAt time.Time) error
	ListPendingOlderThan(ctx context.Context, cutoff time.Time) ([]*models.GameResult, error)
	ListByGroupAndDate(ctx context.Context, groupID int64, gameDate time.Time) ([]*models.GameResult, error)
	ListByGameID(ctx context.Context, gameID int64) ([]*models.GameResult, error)
}

// PlayerRatingRepository is the data access interface for Glicko-2 player ratings.
type PlayerRatingRepository interface {
	GetOrInit(ctx context.Context, groupID, playerID int64) (*models.PlayerRating, error)
	Upsert(ctx context.Context, r *models.PlayerRating) error
	ListByGroup(ctx context.Context, groupID int64) ([]*models.PlayerRating, error)
}

// RatingChangeRepository is the data access interface for rating change history.
type RatingChangeRepository interface {
	Insert(ctx context.Context, change *models.RatingChange) error
	// InsertInTx writes a rating change inside the caller-provided transaction
	// so it lands atomically with the corresponding player_ratings update.
	InsertInTx(ctx context.Context, tx pgx.Tx, change *models.RatingChange) error
	ListByGroupAndDateRange(ctx context.Context, groupID int64, from, to time.Time) ([]*models.RatingChange, error)
}

// GroupRepository also needs SetLastLeaderboardPostedFor.
// (This is an extension added in migration 027 — method added to existing GroupRepository interface below.)

// UserRepository is the subset of storage.UserRepo used by the service layer:
// GameResultService checks results_opt_out; AdminGroupsResolver translates a
// canonical userID to the Telegram external_id needed by GetChatAdministrators.
type UserRepository interface {
	GetByID(ctx context.Context, userID int64) (*models.User, error)
	TelegramID(ctx context.Context, userID int64) (int64, error)
}

// AuditEventRepository is the data access interface for audit events.
type AuditEventRepository interface {
	Insert(ctx context.Context, evt *models.AuditEvent) error
	Query(ctx context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
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
