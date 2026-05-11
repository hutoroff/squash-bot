package participation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	playerRepo        outbound.PlayerRepository
	participationRepo outbound.ParticipationRepository
	guestRepo         outbound.GuestRepository
	notifier          outbound.Notifier
}

func NewService(playerRepo outbound.PlayerRepository, participationRepo outbound.ParticipationRepository, guestRepo outbound.GuestRepository, notifier outbound.Notifier) *Service {
	return &Service{
		playerRepo:        playerRepo,
		participationRepo: participationRepo,
		guestRepo:         guestRepo,
		notifier:          notifier,
	}
}

func (s *Service) Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error) {
	player := &models.Player{TelegramID: telegramID}
	if username != "" {
		player.Username = &username
	}
	if firstName != "" {
		player.FirstName = &firstName
	}
	if lastName != "" {
		player.LastName = &lastName
	}

	saved, err := s.playerRepo.Upsert(ctx, player)
	if err != nil {
		return nil, fmt.Errorf("upsert player: %w", err)
	}

	if err := s.participationRepo.Upsert(ctx, gameID, saved.ID, models.StatusRegistered); err != nil {
		return nil, fmt.Errorf("upsert participation: %w", err)
	}

	slog.Info("Player joined", "player", displayName(username, firstName, lastName), "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if s.notifier != nil {
		go s.notifier.EditGameMessage(context.Background(), gameID)
	}
	return parts, nil
}

func (s *Service) Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error) {
	existingPlayer, err := s.playerRepo.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get player: %w", err)
	}

	participations, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, false, fmt.Errorf("get participations: %w", err)
	}

	registered := false
	for _, p := range participations {
		if p.PlayerID == existingPlayer.ID && p.Status == models.StatusRegistered {
			registered = true
			break
		}
	}
	if !registered {
		return nil, false, nil
	}

	if err := s.participationRepo.Upsert(ctx, gameID, existingPlayer.ID, models.StatusSkipped); err != nil {
		return nil, false, fmt.Errorf("upsert participation: %w", err)
	}

	slog.Info("Player skipped", "player", displayName(username, firstName, lastName), "game_id", gameID)

	updated, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, true, err
	}
	if s.notifier != nil {
		go s.notifier.EditGameMessage(context.Background(), gameID)
	}
	return updated, true, nil
}

func (s *Service) AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	player := &models.Player{TelegramID: telegramID}
	if username != "" {
		player.Username = &username
	}
	if firstName != "" {
		player.FirstName = &firstName
	}
	if lastName != "" {
		player.LastName = &lastName
	}

	saved, err := s.playerRepo.Upsert(ctx, player)
	if err != nil {
		return false, nil, nil, fmt.Errorf("upsert player: %w", err)
	}

	added, err := s.guestRepo.AddGuest(ctx, gameID, saved.ID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("add guest: %w", err)
	}
	if !added {
		return false, nil, nil, nil
	}

	slog.Info("Guest added", "inviter", displayName(username, firstName, lastName), "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("get participations: %w", err)
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("get guests: %w", err)
	}
	if s.notifier != nil {
		go s.notifier.EditGameMessage(context.Background(), gameID)
	}
	return true, parts, guests, nil
}

func (s *Service) RemoveGuest(ctx context.Context, gameID, telegramID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	player, err := s.playerRepo.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil, nil, nil
		}
		return false, nil, nil, fmt.Errorf("get player: %w", err)
	}

	removed, err := s.guestRepo.RemoveLatestGuest(ctx, gameID, player.ID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("remove guest: %w", err)
	}
	if !removed {
		return false, nil, nil, nil
	}

	slog.Info("Guest removed", "inviter_id", telegramID, "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("get participations: %w", err)
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		return false, nil, nil, fmt.Errorf("get guests: %w", err)
	}
	if s.notifier != nil {
		go s.notifier.EditGameMessage(context.Background(), gameID)
	}
	return true, parts, guests, nil
}

func (s *Service) GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error) {
	return s.participationRepo.GetByGame(ctx, gameID)
}

func (s *Service) GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error) {
	return s.guestRepo.GetByGame(ctx, gameID)
}

func (s *Service) GetRegisteredCount(ctx context.Context, gameID int64) (int, error) {
	return s.participationRepo.GetRegisteredCount(ctx, gameID)
}

func (s *Service) GetGuestCount(ctx context.Context, gameID int64) (int, error) {
	return s.guestRepo.GetCountByGame(ctx, gameID)
}

func (s *Service) KickPlayer(ctx context.Context, gameID, telegramID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	player, err := s.playerRepo.GetByTelegramID(ctx, telegramID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("get player: %w", err)
	}

	removed, err := s.participationRepo.DeleteByGameAndPlayer(ctx, gameID, player.ID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("delete participation: %w", err)
	}
	if !removed {
		return nil, nil, false, nil
	}

	slog.Info("Player kicked", "telegram_id", telegramID, "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, true, fmt.Errorf("get participations: %w", err)
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, true, fmt.Errorf("get guests: %w", err)
	}
	return parts, guests, true, nil
}

func (s *Service) KickGuestByID(ctx context.Context, gameID, guestID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	removed, err := s.guestRepo.DeleteByID(ctx, gameID, guestID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("delete guest: %w", err)
	}
	if !removed {
		return nil, nil, false, nil
	}

	slog.Info("Guest kicked", "guest_id", guestID, "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, true, fmt.Errorf("get participations: %w", err)
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, true, fmt.Errorf("get guests: %w", err)
	}
	return parts, guests, true, nil
}

func displayName(username, firstName, lastName string) string {
	if username != "" {
		return "@" + username
	}
	name := firstName
	if lastName != "" {
		if name != "" {
			name += " "
		}
		name += lastName
	}
	return name
}
