package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// adminGroupsCacheTTL bounds how stale an admin-groups answer may be.
// Trade-off: promotions/demotions in Telegram take up to this long to apply.
const adminGroupsCacheTTL = 5 * time.Minute

type adminGroupsCacheEntry struct {
	groups []int64
	at     time.Time
}

// AdminGroupsResolver resolves which groups a Telegram user administers.
// Results are cached per Telegram ID for ttl, because resolving requires one
// GetChatAdministrators call per known group and is on the hot path of every
// settings and audit request.
type AdminGroupsResolver struct {
	groupRepo GroupRepository
	tgAPI     TelegramAPI
	logger    *slog.Logger
	ttl       time.Duration

	mu    sync.Mutex
	cache map[int64]adminGroupsCacheEntry
}

func NewAdminGroupsResolver(groupRepo GroupRepository, tgAPI TelegramAPI, logger *slog.Logger) *AdminGroupsResolver {
	return &AdminGroupsResolver{
		groupRepo: groupRepo,
		tgAPI:     tgAPI,
		logger:    logger,
		ttl:       adminGroupsCacheTTL,
		cache:     map[int64]adminGroupsCacheEntry{},
	}
}

// AdminGroupsFor returns the chat IDs of groups in which tgID is an administrator.
// Per-group errors are logged and skipped so a single unreachable group does not
// fail the whole query.
func (r *AdminGroupsResolver) AdminGroupsFor(ctx context.Context, tgID int64) ([]int64, error) {
	r.mu.Lock()
	if e, ok := r.cache[tgID]; ok && time.Since(e.at) < r.ttl {
		r.mu.Unlock()
		return e.groups, nil
	}
	r.mu.Unlock()

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

	r.mu.Lock()
	r.cache[tgID] = adminGroupsCacheEntry{groups: adminGroups, at: time.Now()}
	r.mu.Unlock()
	return adminGroups, nil
}
