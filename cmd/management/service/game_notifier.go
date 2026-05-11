package service

import (
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/adapters/outbound/telegram"
)

// NewGameNotifier is forwarded to the telegram adapter package.
// Kept as a shim for backward-compatibility until Phase 3.
func NewGameNotifier(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	loc *time.Location,
	logger *slog.Logger,
) *telegram.GameNotifier {
	return telegram.NewGameNotifier(api, gameRepo, partRepo, guestRepo, groupRepo, loc, logger)
}
