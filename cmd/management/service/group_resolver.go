package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

// groupTZByID loads the IANA timezone for the group identified by chatID.
// Returns (loc, true) on success and (nil, false) on any error or not-found.
// Callers must not proceed with timezone-sensitive operations when ok is false.
func groupTZByID(ctx context.Context, groupRepo GroupRepository, chatID int64, defaultLoc *time.Location, logger *slog.Logger) (*time.Location, bool) {
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

// groupLang returns a Localizer for the given chatID's stored language.
// Falls back to English if the group is not found or the call fails.
func groupLang(ctx context.Context, groupRepo GroupRepository, chatID int64) *i18n.Localizer {
	group, err := groupRepo.GetByID(ctx, chatID)
	if err != nil || group == nil {
		return i18n.New(i18n.En)
	}
	return i18n.New(i18n.Normalize(group.Language))
}

// resolveGroupTimezone loads the IANA timezone stored on the group.
// Falls back to defaultLoc when the timezone is empty or invalid.
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
