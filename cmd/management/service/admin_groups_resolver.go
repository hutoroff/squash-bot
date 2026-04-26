package service

import (
	"context"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// AdminGroupsResolver resolves which groups a Telegram user administers.
type AdminGroupsResolver struct {
	groupRepo GroupRepository
	tgAPI     TelegramAPI
	logger    *slog.Logger
}

func NewAdminGroupsResolver(groupRepo GroupRepository, tgAPI TelegramAPI, logger *slog.Logger) *AdminGroupsResolver {
	return &AdminGroupsResolver{groupRepo: groupRepo, tgAPI: tgAPI, logger: logger}
}

// AdminGroupsFor returns the chat IDs of groups in which tgID is an administrator.
// Per-group errors are logged and skipped so a single unreachable group does not
// fail the whole query.
func (r *AdminGroupsResolver) AdminGroupsFor(ctx context.Context, tgID int64) ([]int64, error) {
	groups, err := r.groupRepo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	var adminGroups []int64
	for _, g := range groups {
		members, err := r.tgAPI.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: g.ChatID},
		})
		if err != nil {
			r.logger.Warn("AdminGroupsFor: GetChatAdministrators failed", "group_id", g.ChatID, "err", err)
			continue
		}
		for _, m := range members {
			if m.User.ID == tgID {
				adminGroups = append(adminGroups, g.ChatID)
				break
			}
		}
	}
	return adminGroups, nil
}
