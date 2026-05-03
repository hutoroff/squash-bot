package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/models"
)

var (
	ErrGameNotFound       = errors.New("game not found")
	ErrGameAlreadyPublished = errors.New("game already published")
)

type GameService struct {
	gameRepo        GameRepository
	venueRepo       VenueRepository
	participationRepo ParticipationRepository
	guestRepo       GuestRepository
	groupRepo       GroupRepository
	auditSvc        *AuditService
	api             TelegramAPI
	defaultLoc      *time.Location
	logger          *slog.Logger
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
) *GameService {
	return &GameService{
		gameRepo:          gameRepo,
		venueRepo:         venueRepo,
		participationRepo: participationRepo,
		guestRepo:         guestRepo,
		groupRepo:         groupRepo,
		auditSvc:          auditSvc,
		api:               api,
		defaultLoc:        defaultLoc,
		logger:            logger,
	}
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

func (s *GameService) GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error) {
	return s.gameRepo.GetNextGameForTelegramUser(ctx, telegramID)
}

func (s *GameService) UpdateCourts(ctx context.Context, gameID int64, courts string) error {
	courtsCount := len(strings.Split(courts, ","))
	return s.gameRepo.UpdateCourts(ctx, gameID, courts, courtsCount)
}

func (s *GameService) GetGamesForPlayer(ctx context.Context, playerID int64) ([]models.PlayerGame, error) {
	return s.gameRepo.GetGamesForPlayer(ctx, playerID)
}

// PublishGame sends the game announcement to the group, pins it silently, sets message_id, and records audit.
// Returns ErrGameNotFound if the game doesn't exist, ErrGameAlreadyPublished if already published.
// On send failure, returns an error without touching the DB — the game stays unpublished.
func (s *GameService) PublishGame(ctx context.Context, gameID, actorTgID int64, actorDisplay string) (*models.Game, error) {
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

	s.auditSvc.RecordGamePublished(ctx, gameID, game.ChatID, actorTgID, actorDisplay)

	updated, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return game, nil
	}
	return updated, nil
}
