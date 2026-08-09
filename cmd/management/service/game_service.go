package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrGameNotFound            = errors.New("game not found")
	ErrGameAlreadyPublished    = errors.New("game already published")
	ErrAutoBookingNotAvailable = errors.New("auto-booking not available for this game")
)

// BookGameCourtsResult is returned by BookGameCourts.
type BookGameCourtsResult struct {
	Requested    int
	BookedLabels []string
	Failures     []BookingFailure
}

// CourtBookingInfo is a slim DTO returned by ListActiveCourtBookings.
type CourtBookingInfo struct {
	CourtLabel string `json:"court_label"`
	GameTime   string `json:"game_time"`
	MatchID    string `json:"match_id"`
}

// CourtCancelError carries the court label and the underlying error from a single
// CancelMatch attempt, so the API layer can surface per-court failure details.
type CourtCancelError struct {
	CourtLabel string
	Err        error
}

func (e *CourtCancelError) Error() string {
	return fmt.Sprintf("court %s: %v", e.CourtLabel, e.Err)
}

type GameService struct {
	gameRepo          GameRepository
	venueRepo         VenueRepository
	participationRepo ParticipationRepository
	guestRepo         GuestRepository
	groupRepo         GroupRepository
	auditSvc          *AuditService
	api               TelegramAPI
	notifier          Notifier
	defaultLoc        *time.Location
	logger            *slog.Logger
	// Optional booking deps — nil when booking infrastructure is not configured.
	courtBookingRepo      CourtBookingRepository
	bookingClient         BookingServiceClient
	credService           *VenueCredentialService
	autoBookingResultRepo AutoBookingResultRepository
	resultWindowDays      int
}

func NewGameService(
	gameRepo GameRepository,
	venueRepo VenueRepository,
	participationRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	auditSvc *AuditService,
	api TelegramAPI,
	defaultLoc *time.Location,
	logger *slog.Logger,
	courtBookingRepo CourtBookingRepository,
	bookingClient BookingServiceClient,
	credService *VenueCredentialService,
	autoBookingResultRepo AutoBookingResultRepository,
	resultWindowDays int,
) *GameService {
	return &GameService{
		gameRepo:              gameRepo,
		venueRepo:             venueRepo,
		participationRepo:     participationRepo,
		guestRepo:             guestRepo,
		groupRepo:             groupRepo,
		auditSvc:              auditSvc,
		api:                   api,
		defaultLoc:            defaultLoc,
		logger:                logger,
		courtBookingRepo:      courtBookingRepo,
		bookingClient:         bookingClient,
		credService:           credService,
		autoBookingResultRepo: autoBookingResultRepo,
		resultWindowDays:      resultWindowDays,
	}
}

// SetNotifier injects the Notifier used for async game message edits after BookGameCourts.
// Must be called after construction when the notifier is available (avoids circular deps).
func (s *GameService) SetNotifier(n Notifier) {
	s.notifier = n
}

func (s *GameService) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error) {
	if venueID != nil {
		if _, err := s.venueRepo.GetByIDAndGroupID(ctx, *venueID, chatID); err != nil {
			return nil, fmt.Errorf("venue %d does not belong to group %d", *venueID, chatID)
		}
	}

	courtsCount := len(strings.Split(courts, ","))
	game := &models.Game{
		ChatID:      chatID,
		GameDate:    gameDate,
		Courts:      courts,
		CourtsCount: courtsCount,
		VenueID:     venueID,
	}
	created, err := s.gameRepo.Create(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("create game: %w", err)
	}
	return s.gameRepo.GetByID(ctx, created.ID)
}

func (s *GameService) UpdateMessageID(ctx context.Context, gameID, messageID int64) error {
	return s.gameRepo.UpdateMessageID(ctx, gameID, messageID)
}

func (s *GameService) GetByID(ctx context.Context, id int64) (*models.Game, error) {
	return s.gameRepo.GetByID(ctx, id)
}

func (s *GameService) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGames(ctx)
}

func (s *GameService) GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error) {
	return s.gameRepo.GetUpcomingGamesByChatIDs(ctx, chatIDs)
}

func (s *GameService) GetNextGameForUser(ctx context.Context, userID int64) (*models.Game, error) {
	return s.gameRepo.GetNextGameForUser(ctx, userID)
}

func (s *GameService) UpdateCourts(ctx context.Context, gameID int64, courts string) error {
	courtsCount := len(strings.Split(courts, ","))
	return s.gameRepo.UpdateCourts(ctx, gameID, courts, courtsCount)
}

func (s *GameService) GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetGamesForPlayer(ctx, playerID)
}

func (s *GameService) ListGroupIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	return s.gameRepo.ListGroupIDsForPlayer(ctx, playerID)
}

// PlayerCanAccessGame reports whether the given user is associated with
// gameID's group — i.e. has a participation record in some game within that
// group. Used to authorize the web service's per-game endpoints (IDOR guard).
func (s *GameService) PlayerCanAccessGame(ctx context.Context, userID, gameID int64) (bool, error) {
	return s.gameRepo.PlayerCanAccessGame(ctx, userID, gameID)
}

// GetRecentCompletedGamesForPlayer returns past games for a user in a
// specific group within the configured result-submission window. Used by the
// /result wizard game picker.
func (s *GameService) GetRecentCompletedGamesForPlayer(ctx context.Context, userID, groupID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetRecentCompletedGamesForPlayer(ctx, userID, groupID, s.resultWindowDays)
}

// PublishGame sends the game announcement to the group, pins it silently, sets message_id, and records audit.
// Returns ErrGameNotFound if the game doesn't exist, ErrGameAlreadyPublished if already published.
// On send failure, returns an error without touching the DB — the game stays unpublished.
func (s *GameService) PublishGame(ctx context.Context, gameID, actorUserID int64, actorDisplay string) (*models.Game, error) {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("fetch game %d: %w", gameID, err)
	}
	if game == nil {
		return nil, ErrGameNotFound
	}
	if game.MessageID != nil {
		return nil, ErrGameAlreadyPublished
	}

	groupTZ, ok := groupTZByID(ctx, s.groupRepo, game.ChatID, s.defaultLoc, s.logger)
	if !ok {
		groupTZ = s.defaultLoc
	}

	lz := groupLang(ctx, s.groupRepo, game.ChatID)

	participations, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		s.logger.Error("PublishGame: get participations", "game_id", gameID, "err", err)
		participations = nil
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		s.logger.Error("PublishGame: get guests", "game_id", gameID, "err", err)
		guests = nil
	}

	msgText := gameformat.FormatGameMessage(game, participations, guests, groupTZ, time.Now().UTC(), lz)
	keyboard := gameformat.GameKeyboard(game.ID, lz)

	announcement := tgbotapi.NewMessage(game.ChatID, msgText)
	announcement.ReplyMarkup = keyboard
	sent, err := s.api.Send(announcement)
	if err != nil {
		s.logger.Error("PublishGame: send announcement", "game_id", gameID, "chat_id", game.ChatID, "err", err)
		return nil, fmt.Errorf("send game announcement: %w", err)
	}

	pin := tgbotapi.PinChatMessageConfig{
		ChatID:              game.ChatID,
		MessageID:           sent.MessageID,
		DisableNotification: true,
	}
	if _, err := s.api.Request(pin); err != nil {
		s.logger.Error("PublishGame: pin message", "game_id", gameID, "err", err)
		// Non-fatal: message exists in chat.
	}

	if err := s.gameRepo.UpdateMessageID(ctx, gameID, int64(sent.MessageID)); err != nil {
		s.logger.Error("PublishGame: update message_id", "game_id", gameID, "message_id", sent.MessageID, "err", err)
		// The message is live in the chat but message_id is not persisted — delete it to
		// prevent orphaned announcements and duplicate publishes on retry.
		del := tgbotapi.NewDeleteMessage(game.ChatID, sent.MessageID)
		if _, delErr := s.api.Request(del); delErr != nil {
			s.logger.Error("PublishGame: delete orphaned message after persist failure",
				"game_id", gameID, "message_id", sent.MessageID, "err", delErr)
		}
		return nil, fmt.Errorf("persist message_id: %w", err)
	}

	s.auditSvc.RecordGamePublished(ctx, gameID, game.ChatID, actorUserID, actorDisplay)

	updated, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return game, nil
	}
	return updated, nil
}

// BookGameCourts books `count` additional courts for an existing game using the venue's
// auto-booking configuration. The booked court labels are appended to game.Courts.
//
// Returns ErrGameNotFound when the game doesn't exist, ErrAutoBookingNotAvailable when the
// game has no venue or the venue's auto-booking is disabled.
// Sanity check: rejects games whose time is exactly 00:00 (likely unset).
func (s *GameService) BookGameCourts(ctx context.Context, gameID int64, count int, actorUserID int64, actorDisplay string, credCooldown time.Duration) (*BookGameCourtsResult, error) {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("fetch game %d: %w", gameID, err)
	}
	if game == nil {
		return nil, ErrGameNotFound
	}
	if game.Venue == nil || !game.Venue.AutoBookingEnabled {
		return nil, ErrAutoBookingNotAvailable
	}
	if s.bookingClient == nil || s.credService == nil {
		return nil, ErrAutoBookingNotAvailable
	}

	groupTZ, ok := groupTZByID(ctx, s.groupRepo, game.ChatID, s.defaultLoc, s.logger)
	if !ok {
		groupTZ = s.defaultLoc
	}

	gameInTZ := game.GameDate.In(groupTZ)
	// Sanity check: game time 00:00 almost certainly means the time was never set.
	if gameInTZ.Hour() == 0 && gameInTZ.Minute() == 0 {
		return nil, fmt.Errorf("game time is 00:00 — cannot infer booking time slot")
	}

	gameTime := gameInTZ.Format("15:04")
	gameDateStr := gameInTZ.Format("2006-01-02")
	gameDate := time.Date(gameInTZ.Year(), gameInTZ.Month(), gameInTZ.Day(), 0, 0, 0, 0, groupTZ)

	deps := bookingDeps{
		bookingClient:    s.bookingClient,
		credService:      s.credService,
		courtBookingRepo: s.courtBookingRepo,
		auditSvc:         s.auditSvc,
		credCooldown:     credCooldown,
		logger:           s.logger,
	}

	res, err := bookFreeCourts(ctx, deps, game.Venue, game.ChatID, gameDate, gameDateStr, gameTime, groupTZ, count)
	if err != nil {
		return nil, fmt.Errorf("book free courts: %w", err)
	}

	result := &BookGameCourtsResult{
		Requested:    count,
		BookedLabels: res.BookedLabels,
		Failures:     res.Failures,
	}

	if len(res.BookedLabels) > 0 {
		existingCourts := splitCourts(game.Courts)
		existing := make(map[string]int, len(existingCourts))
		for _, c := range existingCourts {
			existing[c]++
		}
		allCourts := existingCourts
		for _, label := range res.BookedLabels {
			if existing[label] > 0 {
				existing[label]--
				continue
			}
			allCourts = append(allCourts, label)
		}
		newCourtsStr := strings.Join(allCourts, ",")
		if updateErr := s.gameRepo.UpdateCourts(ctx, gameID, newCourtsStr, len(allCourts)); updateErr != nil {
			return result, fmt.Errorf("update game courts: %w", updateErr)
		}

		// Upsert auto_booking_results: only insert when none exists yet for this slot.
		if s.autoBookingResultRepo != nil {
			existing, _ := s.autoBookingResultRepo.GetByVenueAndDateAndTime(ctx, game.Venue.ID, gameDate, gameTime)
			if existing == nil {
				courtsStr := strings.Join(res.BookedLabels, ",")
				resultID, saveErr := s.autoBookingResultRepo.Save(ctx, game.Venue.ID, gameDate, gameTime, courtsStr, len(res.BookedLabels))
				if saveErr != nil {
					s.logger.Error("BookGameCourts: save auto_booking_result", "game_id", gameID, "err", saveErr)
				} else {
					if linkErr := s.autoBookingResultRepo.SetGameID(ctx, resultID, gameID); linkErr != nil {
						s.logger.Error("BookGameCourts: set game_id on result", "result_id", resultID, "err", linkErr)
					}
				}
			}
			// If a row already exists: leave it untouched. court_bookings carry game_time for
			// cancellation scoping, so the existing result's courts/courts_count may understate
			// reality, but cancellation still works correctly via court_bookings.
		}

		if s.auditSvc != nil {
			s.auditSvc.RecordCourtsAutoBooked(ctx, game.ChatID, actorUserID, actorDisplay, gameID,
				game.Venue.Name, gameDateStr, len(res.BookedLabels), count, res.BookedLabels)
		}

		// Async refresh of the game message in the group chat.
		// Use context.Background() so the goroutine is not tied to the HTTP request lifetime.
		if s.notifier != nil {
			go s.notifier.EditGameMessage(context.Background(), gameID)
		}
	}

	return result, nil
}

// activeBookingsByLabels returns active bookings for the given court labels on a game,
// using time-slot scoping when the game is linked to an auto-booking result.
// This prevents false matches when a venue hosts multiple sessions on the same date
// that share the same court labels.
func (s *GameService) activeBookingsByLabels(ctx context.Context, game *models.Game, labels []string) ([]*models.CourtBooking, error) {
	var gameTime string
	if s.autoBookingResultRepo != nil {
		if abr, err := s.autoBookingResultRepo.GetByGameID(ctx, game.ID); err == nil && abr != nil {
			gameTime = abr.GameTime
		}
	}

	if gameTime != "" {
		// Time-slot scoping: fetch only bookings for this game's time slot, then filter by label.
		all, err := s.courtBookingRepo.GetByVenueAndDateAndTime(ctx, game.Venue.ID, game.GameDate, gameTime)
		if err != nil {
			return nil, err
		}
		labelSet := make(map[string]bool, len(labels))
		for _, l := range labels {
			labelSet[l] = true
		}
		var out []*models.CourtBooking
		for _, b := range all {
			if labelSet[b.CourtLabel] {
				out = append(out, b)
			}
		}
		return out, nil
	}

	// No game_time known (manually created game or legacy data) — fall back to date+label matching.
	return s.courtBookingRepo.GetActiveByVenueDateAndLabels(ctx, game.Venue.ID, game.GameDate, labels)
}

// ListActiveCourtBookings returns slim booking info for active (non-canceled) bookings whose
// court_label matches one of the provided labels. Returns empty slice when booking
// infrastructure is absent or the game has no venue.
func (s *GameService) ListActiveCourtBookings(ctx context.Context, gameID int64, courts []string) ([]CourtBookingInfo, error) {
	if len(courts) == 0 || s.courtBookingRepo == nil {
		return nil, nil
	}
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("get game %d: %w", gameID, err)
	}
	if game.Venue == nil || game.Venue.ID == 0 {
		return nil, nil
	}
	entries, err := s.activeBookingsByLabels(ctx, game, courts)
	if err != nil {
		return nil, fmt.Errorf("get active bookings: %w", err)
	}
	result := make([]CourtBookingInfo, 0, len(entries))
	for _, e := range entries {
		result = append(result, CourtBookingInfo{
			CourtLabel: e.CourtLabel,
			GameTime:   e.GameTime,
			MatchID:    e.MatchID,
		})
	}
	return result, nil
}

// RemoveCourtsAndCancelBookings cancels active Eversports bookings for courts that are
// being removed (present in game.Courts but not in newCourts), then persists newCourts.
// The DB update always runs regardless of cancellation failures — the admin's chosen
// court list is persisted and partial failures are reported back via CourtCancelError.
func (s *GameService) RemoveCourtsAndCancelBookings(ctx context.Context, gameID int64, newCourts string) (canceledLabels []string, cancelErrors []CourtCancelError, err error) {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return nil, nil, fmt.Errorf("get game %d: %w", gameID, err)
	}

	current := splitCourts(game.Courts)
	incoming := splitCourts(newCourts)

	// Multiset diff: labels in current but not in incoming.
	incomingCount := make(map[string]int, len(incoming))
	for _, c := range incoming {
		incomingCount[c]++
	}
	var removed []string
	for _, c := range current {
		if incomingCount[c] > 0 {
			incomingCount[c]--
		} else {
			removed = append(removed, c)
		}
	}

	if len(removed) == 0 || game.Venue == nil || game.Venue.ID == 0 ||
		s.courtBookingRepo == nil || s.bookingClient == nil {
		return nil, nil, s.gameRepo.UpdateCourts(ctx, gameID, newCourts, len(incoming))
	}

	// Unique labels for the repo query; cancel count per label caps how many we cancel.
	cancelQuota := make(map[string]int, len(removed))
	uniqueLabels := make([]string, 0, len(removed))
	for _, l := range removed {
		if cancelQuota[l] == 0 {
			uniqueLabels = append(uniqueLabels, l)
		}
		cancelQuota[l]++
	}

	entries, err := s.activeBookingsByLabels(ctx, game, uniqueLabels)
	if err != nil {
		return nil, nil, fmt.Errorf("get active bookings: %w", err)
	}

	for _, entry := range entries {
		if cancelQuota[entry.CourtLabel] <= 0 {
			continue
		}
		cancelQuota[entry.CourtLabel]--
		login, password := "", ""
		if entry.CredentialID != nil && s.credService != nil {
			cred, credErr := s.credService.GetDecryptedByID(ctx, *entry.CredentialID)
			if credErr != nil {
				s.logger.Error("RemoveCourtsAndCancelBookings: get credential",
					"game_id", gameID, "cred_id", *entry.CredentialID, "err", credErr)
				cancelErrors = append(cancelErrors, CourtCancelError{CourtLabel: entry.CourtLabel, Err: credErr})
				continue
			}
			if cred != nil {
				login, password = cred.Login, cred.Password
			}
		}
		if login == "" {
			noCredErr := fmt.Errorf("no credentials available")
			s.logger.Warn("RemoveCourtsAndCancelBookings: no credentials for court",
				"game_id", gameID, "court_label", entry.CourtLabel)
			cancelErrors = append(cancelErrors, CourtCancelError{CourtLabel: entry.CourtLabel, Err: noCredErr})
			continue
		}
		if cancelErr := s.bookingClient.CancelMatch(ctx, entry.MatchID, login, password); cancelErr != nil {
			s.logger.Error("RemoveCourtsAndCancelBookings: cancel match",
				"game_id", gameID, "court_label", entry.CourtLabel, "match_id", entry.MatchID, "err", cancelErr)
			cancelErrors = append(cancelErrors, CourtCancelError{CourtLabel: entry.CourtLabel, Err: cancelErr})
			continue
		}
		if markErr := s.courtBookingRepo.MarkCanceled(ctx, entry.MatchID); markErr != nil {
			s.logger.Error("RemoveCourtsAndCancelBookings: mark canceled",
				"game_id", gameID, "match_id", entry.MatchID, "err", markErr)
		}
		if s.auditSvc != nil {
			s.auditSvc.RecordCourtCanceled(ctx, game.Venue.ID, game.ChatID, game.Venue.Name, entry.CourtLabel, game.GameDate)
		}
		canceledLabels = append(canceledLabels, entry.CourtLabel)
	}

	if updateErr := s.gameRepo.UpdateCourts(ctx, gameID, newCourts, len(incoming)); updateErr != nil {
		return canceledLabels, cancelErrors, fmt.Errorf("update courts: %w", updateErr)
	}
	return canceledLabels, cancelErrors, nil
}
