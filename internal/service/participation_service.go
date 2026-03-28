package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type ParticipationService struct {
	playerRepo        *storage.PlayerRepo
	participationRepo *storage.ParticipationRepo
	guestRepo         *storage.GuestRepo
}

func NewParticipationService(playerRepo *storage.PlayerRepo, participationRepo *storage.ParticipationRepo, guestRepo *storage.GuestRepo) *ParticipationService {
	return &ParticipationService{
		playerRepo:        playerRepo,
		participationRepo: participationRepo,
		guestRepo:         guestRepo,
	}
}

func (s *ParticipationService) Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error) {
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

	return s.participationRepo.GetByGame(ctx, gameID)
}

// Skip marks a player as skipped. Returns (participations, skipped, error).
// skipped=false means the player was not registered, so no change was made.
// Guests previously added by this player are independent and are not affected.
func (s *ParticipationService) Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error) {
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
	return updated, true, err
}

// AddGuest records a +1 for the given Telegram user and returns the refreshed
// participant and guest lists for message update. Any group member may add guests
// regardless of their own registration status; guests are managed independently.
func (s *ParticipationService) AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, []*models.GuestParticipation, error) {
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
		return nil, nil, fmt.Errorf("upsert player: %w", err)
	}

	if err := s.guestRepo.AddGuest(ctx, gameID, saved.ID); err != nil {
		return nil, nil, fmt.Errorf("add guest: %w", err)
	}

	slog.Info("Guest added", "inviter", displayName(username, firstName, lastName), "game_id", gameID)

	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, fmt.Errorf("get participations: %w", err)
	}
	guests, err := s.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, nil, fmt.Errorf("get guests: %w", err)
	}
	return parts, guests, nil
}

// RemoveGuest removes the most recently added guest for the given Telegram user.
// Returns (removed, participations, guests, error). removed=false means the user
// had no guests to remove. Only the player who added a guest can remove it.
func (s *ParticipationService) RemoveGuest(ctx context.Context, gameID, telegramID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
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
	return true, parts, guests, nil
}

// GetGuests returns all guests for the given game.
func (s *ParticipationService) GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error) {
	return s.guestRepo.GetByGame(ctx, gameID)
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
