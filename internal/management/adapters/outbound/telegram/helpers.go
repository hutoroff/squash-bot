package telegram

import (
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

func resolveGroupTimezone(group *models.Group, defaultLoc *time.Location, logger *slog.Logger) *time.Location {
	if group.Timezone == "" {
		return defaultLoc
	}
	loc, err := time.LoadLocation(group.Timezone)
	if err != nil {
		logger.Warn("invalid group timezone, using service default",
			"timezone", group.Timezone, "chat_id", group.ChatID)
		return defaultLoc
	}
	return loc
}
