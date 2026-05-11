package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

func groupTZByID(ctx context.Context, groupRepo outbound.GroupRepository, chatID int64, defaultLoc *time.Location, logger *slog.Logger) (*time.Location, bool) {
	group, err := groupRepo.GetByID(ctx, chatID)
	if err != nil {
		logger.Error("cannot resolve group timezone", "chat_id", chatID, "err", err)
		return nil, false
	}
	if group == nil {
		logger.Error("cannot resolve group timezone: group not found", "chat_id", chatID)
		return nil, false
	}
	return resolveGroupTimezone(group, defaultLoc, logger), true
}

func groupLang(ctx context.Context, groupRepo outbound.GroupRepository, chatID int64) *i18n.Localizer {
	group, err := groupRepo.GetByID(ctx, chatID)
	if err != nil || group == nil {
		return i18n.New(i18n.En)
	}
	return i18n.New(i18n.Normalize(group.Language))
}

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
