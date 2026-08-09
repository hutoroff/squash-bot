package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// countingTelegramAPI records how many GetChatAdministrators calls were made
// and reports adminID as an administrator of every group.
type countingTelegramAPI struct {
	adminID int64
	calls   int
}

func (c *countingTelegramAPI) Send(_ tgbotapi.Chattable) (tgbotapi.Message, error) {
	return tgbotapi.Message{}, nil
}

func (c *countingTelegramAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (c *countingTelegramAPI) GetChatAdministrators(_ tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	c.calls++
	return []tgbotapi.ChatMember{{User: &tgbotapi.User{ID: c.adminID}}}, nil
}

func newTestResolver(adminID int64, chatIDs ...int64) (*AdminGroupsResolver, *countingTelegramAPI) {
	groups := make([]models.Group, 0, len(chatIDs))
	for _, id := range chatIDs {
		groups = append(groups, models.Group{ChatID: id})
	}
	api := &countingTelegramAPI{adminID: adminID}
	r := NewAdminGroupsResolver(
		&stubGroupRepoAnnounce{groups: groups},
		api,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return r, api
}

func TestAdminGroupsFor_CachesWithinTTL(t *testing.T) {
	r, api := newTestResolver(42, -100, -200)

	first, err := r.AdminGroupsFor(context.Background(), 42)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("want 2 admin groups, got %v", first)
	}
	if api.calls != 2 {
		t.Fatalf("want 2 Telegram calls, got %d", api.calls)
	}

	second, err := r.AdminGroupsFor(context.Background(), 42)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if api.calls != 2 {
		t.Errorf("cached call should not hit Telegram: got %d calls", api.calls)
	}
	if len(second) != 2 {
		t.Errorf("cached result mismatch: %v", second)
	}
}

func TestAdminGroupsFor_ExpiredCacheRefetches(t *testing.T) {
	r, api := newTestResolver(42, -100)
	r.ttl = 0 // every entry is immediately stale

	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if api.calls != 2 {
		t.Errorf("want 2 Telegram calls after TTL expiry, got %d", api.calls)
	}
}

func TestAdminGroupsFor_CacheIsPerUser(t *testing.T) {
	r, api := newTestResolver(42, -100)

	if _, err := r.AdminGroupsFor(context.Background(), 42); err != nil {
		t.Fatalf("admin call: %v", err)
	}
	other, err := r.AdminGroupsFor(context.Background(), 99)
	if err != nil {
		t.Fatalf("other call: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("non-admin should have no groups, got %v", other)
	}
	if api.calls != 2 {
		t.Errorf("want a fresh lookup per user, got %d calls", api.calls)
	}
}
