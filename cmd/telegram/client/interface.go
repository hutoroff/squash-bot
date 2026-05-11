package client

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ManagementClient is the interface the telegram Bot uses to communicate with
// the management service. *Client satisfies it structurally.
type ManagementClient interface {
	// Games
	CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64, actorTgID int64, actorDisplay string) (*models.Game, error)
	GetGameByID(ctx context.Context, id int64) (*models.Game, error)
	UpdateMessageID(ctx context.Context, gameID, messageID int64) error
	UpdateCourts(ctx context.Context, gameID, groupID int64, courts, actorDisplay string, actorTgID int64) error
	GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error)
	GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error)

	// Participations
	Join(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error)
	Skip(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error)
	AddGuest(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	RemoveGuest(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
	GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
	KickPlayer(ctx context.Context, gameID, telegramID, groupID, actorTgID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
	KickGuestByID(ctx context.Context, gameID, guestID, groupID, actorTgID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)

	// Groups
	UpsertGroup(ctx context.Context, chatID int64, title string, botIsAdmin bool, actorTgID int64, actorDisplay string, isNewJoin bool) error
	RemoveGroup(ctx context.Context, chatID, actorTgID int64, actorDisplay, groupTitle string) error
	GetGroups(ctx context.Context) ([]models.Group, error)
	GroupExists(ctx context.Context, chatID int64) (bool, error)
	GetGroupByID(ctx context.Context, chatID int64) (*models.Group, error)
	SetGroupLanguage(ctx context.Context, chatID int64, language string, actorTgID int64, actorDisplay string) error
	SetGroupTimezone(ctx context.Context, chatID int64, timezone string, actorTgID int64, actorDisplay string) error
	SetGroupChangelog(ctx context.Context, chatID int64, enabled bool, actorTgID int64, actorDisplay string) error

	// Venues
	CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingCourtsCount int, actorTgID int64, actorDisplay string) (*models.Venue, error)
	GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error)
	GetVenueByID(ctx context.Context, id int64) (*models.Venue, error)
	UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingCourtsCount int, actorTgID int64, actorDisplay string) (*models.Venue, error)
	DeleteVenue(ctx context.Context, id, groupID, actorTgID int64, actorDisplay string) error

	// Venue credentials
	AddVenueCredential(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int, actorTgID int64, actorDisplay string) (*models.VenueCredential, error)
	ListVenueCredentials(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error)
	DeleteVenueCredential(ctx context.Context, venueID, credentialID, groupID, actorTgID int64, actorDisplay string) error
	ListVenueCredentialPriorities(ctx context.Context, venueID, groupID int64) ([]int, error)

	// Scheduler
	TriggerScheduledEvent(ctx context.Context, event string) error

	// PublishGame sends the game announcement and pins it. Returns ErrAlreadyPublished (HTTP 409) if already done.
	PublishGame(ctx context.Context, gameID, actorTgID int64, actorDisplay string) (*models.Game, error)

	// ListActiveCourtBookings returns active Eversports bookings for the given courts of a game.
	ListActiveCourtBookings(ctx context.Context, gameID int64, courts []string) ([]CourtBookingInfo, error)

	// UpdateCourtsAndCancelBookings cancels active bookings for removed courts then updates courts.
	// On partial cancellation failure, failed is non-empty and the courts update still happened.
	UpdateCourtsAndCancelBookings(ctx context.Context, gameID, groupID int64, newCourts, actorDisplay string, actorTgID int64) (canceledLabels []string, failed []CancelFailure, err error)

	// BookGameCourts books N courts for a game via auto-booking infrastructure.
	// Returns ErrAutoBookingNotAvailable (HTTP 409) if the game has no usable venue/credentials.
	BookGameCourts(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string, count int) (*BookGameCourtsResult, error)

	// GetVenueBookingReadiness checks whether a venue can auto-book courts right now.
	GetVenueBookingReadiness(ctx context.Context, venueID, groupID int64) (*BookingReadiness, error)
}
