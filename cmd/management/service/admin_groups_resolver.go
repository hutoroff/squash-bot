package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"
)

// adminGroupsCacheTTL bounds how stale an admin-groups answer may be.
// Trade-off: promotions/demotions in Telegram take up to this long to apply.
const adminGroupsCacheTTL = 5 * time.Minute

type adminGroupsCacheEntry struct {
	groups []int64
	at     time.Time
}

// AdminGroupsResolver resolves which groups a user administers. Group-admin
// authorization stays Telegram-derived (GetChatAdministrators); the resolver
// translates the canonical userID to its Telegram external_id first, since
// that's the only identity Telegram itself knows about.
// Results are cached per userID for ttl, because resolving requires one
// GetChatAdministrators call per known group and is on the hot path of every
// settings and audit request.
type AdminGroupsResolver struct {
	groupRepo GroupRepository
	userRepo  UserRepository
	tgAPI     TelegramAPI
	logger    *slog.Logger
	ttl       time.Duration

	mu    sync.Mutex
	cache map[int64]adminGroupsCacheEntry

	// sf collapses concurrent cold-cache lookups for the same userID into
	// one scan; the settings page fires several authorized requests at once.
	sf singleflight.Group
}

func NewAdminGroupsResolver(groupRepo GroupRepository, userRepo UserRepository, tgAPI TelegramAPI, logger *slog.Logger) *AdminGroupsResolver {
	return &AdminGroupsResolver{
		groupRepo: groupRepo,
		userRepo:  userRepo,
		tgAPI:     tgAPI,
		logger:    logger,
		ttl:       adminGroupsCacheTTL,
		cache:     map[int64]adminGroupsCacheEntry{},
	}
}

// AdminGroupsFor returns the chat IDs of groups in which userID is an
// administrator. Returns an empty slice (not an error) when the user has no
// telegram identity. Per-group errors are logged and skipped so a single
// unreachable group does not fail the whole query.
func (r *AdminGroupsResolver) AdminGroupsFor(ctx context.Context, userID int64) ([]int64, error) {
	r.mu.Lock()
	if e, ok := r.cache[userID]; ok && time.Since(e.at) < r.ttl {
		r.mu.Unlock()
		return e.groups, nil
	}
	r.mu.Unlock()

	tgID, err := r.userRepo.TelegramID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return []int64{}, nil
	}
	if err != nil {
		return nil, err
	}

	v, err, _ := r.sf.Do(strconv.FormatInt(userID, 10), func() (any, error) {
		groups, err := r.groupRepo.GetAll(ctx)
		if err != nil {
			return nil, err
		}
		var adminGroups []int64
		partial := false
		for _, g := range groups {
			members, err := r.tgAPI.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
				ChatConfig: tgbotapi.ChatConfig{ChatID: g.ChatID},
			})
			if err != nil {
				r.logger.Warn("AdminGroupsFor: GetChatAdministrators failed", "group_id", g.ChatID, "err", err)
				partial = true
				continue
			}
			for _, m := range members {
				if m.User.ID == tgID {
					adminGroups = append(adminGroups, g.ChatID)
					break
				}
			}
		}

		// A partial answer (rate limit, unreachable group) is returned but not
		// cached — otherwise a missing group stays missing for the whole TTL.
		if !partial {
			r.mu.Lock()
			r.cache[userID] = adminGroupsCacheEntry{groups: adminGroups, at: time.Now()}
			r.mu.Unlock()
		}
		return adminGroups, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]int64), nil
}
