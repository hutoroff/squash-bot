package client

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ResolvedUser is the canonical identity for a Telegram user, returned by
// ResolveUser. Every handler that hits a user-keyed management method must
// resolve first and use UserID (never the raw Telegram ID) for that call.
type ResolvedUser struct {
	UserID        int64
	PlayerID      *int64
	DisplayName   string
	IsServerOwner bool
}

// ManagementClient is the interface the telegram Bot uses to communicate with
// the management service. *Client satisfies it structurally.
type ManagementClient interface {
	// Identity
	ResolveUser(ctx context.Context, tgID int64, username, firstName, lastName string) (*ResolvedUser, error)
	GetUser(ctx context.Context, userID int64) (*models.User, error)
	SetUserDMLanguage(ctx context.Context, userID int64, language string) error
	SetUserResultsOptOut(ctx context.Context, userID int64, optOut bool) error

	// Games
	CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts, sport string, venueID *int64, actorUserID int64, actorDisplay string) (*models.Game, error)
	GetGameByID(ctx context.Context, id int64) (*models.Game, error)
	UpdateMessageID(ctx context.Context, gameID, messageID int64) error
	UpdateCourts(ctx context.Context, gameID, groupID int64, courts, actorDisplay string, actorUserID int64) error
	GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error)
	GetNextGameForUser(ctx context.Context, userID int64) (*models.Game, error)

	// Participations
	Join(ctx context.Context, gameID, chatID, userID int64) ([]*models.GameParticipation, error)
	Skip(ctx context.Context, gameID, chatID, userID int64) ([]*models.GameParticipation, bool, error)
	AddGuest(ctx context.Context, gameID, chatID, userID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	RemoveGuest(ctx context.Context, gameID, chatID, userID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error)
	GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error)
	GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error)
	KickPlayer(ctx context.Context, gameID, playerID, groupID, actorUserID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)
	KickGuestByID(ctx context.Context, gameID, guestID, groupID, actorUserID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error)

	// Groups
	UpsertGroup(ctx context.Context, chatID int64, title string, botIsAdmin bool, actorUserID int64, actorDisplay string, isNewJoin bool) error
	RemoveGroup(ctx context.Context, chatID, actorUserID int64, actorDisplay, groupTitle string) error
	GetGroups(ctx context.Context) ([]models.Group, error)
	GroupExists(ctx context.Context, chatID int64) (bool, error)
	GetGroupByID(ctx context.Context, chatID int64) (*models.Group, error)
	SetGroupLanguage(ctx context.Context, chatID int64, language string, actorUserID int64, actorDisplay string) error
	SetGroupTimezone(ctx context.Context, chatID int64, timezone string, actorUserID int64, actorDisplay string) error
	SetGroupChangelog(ctx context.Context, chatID int64, enabled bool, actorUserID int64, actorDisplay string) error
	SetGroupLeaderboardNotifications(ctx context.Context, chatID int64, enabled bool, actorUserID int64, actorDisplay string) error
	SetGroupAutoBookingAllowed(ctx context.Context, chatID int64, allowed bool, actorUserID int64, actorDisplay string) error

	// Venues
	CreateVenue(ctx context.Context, params VenueParams) (*models.Venue, error)
	GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error)
	GetVenueByID(ctx context.Context, id int64) (*models.Venue, error)
	UpdateVenue(ctx context.Context, id int64, params VenueParams) (*models.Venue, error)
	DeleteVenue(ctx context.Context, id, groupID, actorUserID int64, actorDisplay string) error

	// Venue credentials
	AddVenueCredential(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int, actorUserID int64, actorDisplay string) (*models.VenueCredential, error)
	ListVenueCredentials(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error)
	DeleteVenueCredential(ctx context.Context, venueID, credentialID, groupID, actorUserID int64, actorDisplay string) error
	ListVenueCredentialPriorities(ctx context.Context, venueID, groupID int64) ([]int, error)

	// PublishGame sends the game announcement and pins it. Returns ErrAlreadyPublished (HTTP 409) if already done.
	PublishGame(ctx context.Context, gameID, actorUserID int64, actorDisplay string) (*models.Game, error)

	// ListActiveCourtBookings returns active Eversports bookings for the given courts of a game.
	ListActiveCourtBookings(ctx context.Context, gameID int64, courts []string) ([]CourtBookingInfo, error)

	// UpdateCourtsAndCancelBookings cancels active bookings for removed courts then updates courts.
	// On partial cancellation failure, failed is non-empty and the courts update still happened.
	UpdateCourtsAndCancelBookings(ctx context.Context, gameID, groupID int64, newCourts, actorDisplay string, actorUserID int64) (canceledLabels []string, failed []CancelFailure, err error)

	// BookGameCourts books N courts for a game via auto-booking infrastructure.
	// Returns ErrAutoBookingNotAvailable (HTTP 409) if the game has no usable venue/credentials.
	BookGameCourts(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string, count int) (*BookGameCourtsResult, error)

	// GetVenueBookingReadiness checks whether a venue can auto-book courts right now.
	GetVenueBookingReadiness(ctx context.Context, venueID, groupID int64) (*BookingReadiness, error)

	// Leaderboard
	GetLeaderboard(ctx context.Context, groupID int64) ([]LeaderboardEntry, error)
	GetPlayerGroups(ctx context.Context, userID int64) ([]models.Group, error)

	// Game results
	SubmitGameResult(ctx context.Context, gameID, authorUserID, opponentPlayerID int64, winnerPlayerID *int64, score, actorDisplay string, scoreKind models.ScoreKind) (*GameResultDTO, error)
	GetGameResult(ctx context.Context, id int64) (*GameResultDTO, error)
	SetGameResultApprovalMessage(ctx context.Context, id, chatID int64, messageID int) error
	ApproveGameResult(ctx context.Context, id, actorUserID int64, actorDisplay string) (*GameResultDTO, error)
	RejectGameResult(ctx context.Context, id, actorUserID int64, actorDisplay string) (*GameResultDTO, error)
	CancelGameResult(ctx context.Context, id, actorUserID int64, actorDisplay string) (*GameResultDTO, error)
	GetRecentCompletedGames(ctx context.Context, userID, groupID int64) ([]models.PlayerGame, error)
}
