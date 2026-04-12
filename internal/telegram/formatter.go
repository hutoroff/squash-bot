package telegram

import (
	"time"

	"github.com/vkhutorov/squash_bot/internal/gameformat"
	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

// FormatGameMessage produces the announcement text for a squash game.
// Delegates to the shared gameformat package.
func FormatGameMessage(game *models.Game, participants []*models.GameParticipation, guests []*models.GuestParticipation, loc *time.Location, now time.Time, lz *i18n.Localizer) string {
	return gameformat.FormatGameMessage(game, participants, guests, loc, now, lz)
}
