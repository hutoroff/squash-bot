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
// Returns (false, nil, nil, nil) when the game is already at full capacity.
func (s *ParticipationService) AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
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
	return true, parts, guests, nil
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

// GetParticipations returns all participations for the given game.
func (s *ParticipationService) GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error) {
	return s.participationRepo.GetByGame(ctx, gameID)
}

// GetGuests returns all guests for the given game.
func (s *ParticipationService) GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error) {
	return s.guestRepo.GetByGame(ctx, gameID)
}

// GetRegisteredCount returns the number of registered (non-skipped) players for the given game.
func (s *ParticipationService) GetRegisteredCount(ctx context.Context, gameID int64) (int, error) {
	return s.participationRepo.GetRegisteredCount(ctx, gameID)
}

// GetGuestCount returns the total number of guests for the given game.
func (s *ParticipationService) GetGuestCount(ctx context.Context, gameID int64) (int, error) {
	return s.guestRepo.GetCountByGame(ctx, gameID)
}

// KickPlayer removes a player's participation from a game by their Telegram ID.
// Returns the refreshed participant and guest lists.
// Returns nil, nil, false, nil if the player is not in the database.
func (s *ParticipationService) KickPlayer(ctx context.Context, gameID, telegramID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
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

// KickGuestByID removes a specific guest participation by its ID.
// Returns the refreshed participant and guest lists.
func (s *ParticipationService) KickGuestByID(ctx context.Context, gameID, guestID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
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
